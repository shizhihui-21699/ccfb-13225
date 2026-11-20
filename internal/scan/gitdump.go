package scan

// git 源码还原：优先解析 .git/index 拿到工作区文件清单与 blob sha，
// 逐个拉取 loose object 解压写盘；index 不可用时退化为 HEAD→commit→tree 递归遍历。
// 仅支持 loose object（新鲜提交的仓库都是 loose 的）；packfile 暂不支持。

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type gitObject struct {
	typ     string // blob / tree / commit
	payload []byte
}

func fetchGitObject(client *http.Client, base, sha string) (*gitObject, error) {
	url := fmt.Sprintf("%s/.git/objects/%s/%s", base, sha[:2], sha[2:])
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("object %s: HTTP %d", sha, resp.StatusCode)
	}
	zr, err := zlib.NewReader(resp.Body)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, 64<<20))
	if err != nil {
		return nil, err
	}
	nul := bytes.IndexByte(raw, 0)
	if nul < 0 {
		return nil, fmt.Errorf("object %s: 缺少头部", sha)
	}
	header := string(raw[:nul]) // "blob 1234"
	typ := strings.SplitN(header, " ", 2)[0]
	return &gitObject{typ: typ, payload: raw[nul+1:]}, nil
}

// parseGitIndex 解析 .git/index v2，返回 (sha, path) 列表
func parseGitIndex(data []byte) ([][2]string, error) {
	if len(data) < 12 || string(data[:4]) != "DIRC" {
		return nil, fmt.Errorf("不是 git index 文件")
	}
	version := binary.BigEndian.Uint32(data[4:8])
	if version != 2 && version != 3 {
		return nil, fmt.Errorf("暂不支持 index v%d", version)
	}
	count := binary.BigEndian.Uint32(data[8:12])
	off := 12
	var out [][2]string
	for i := uint32(0); i < count; i++ {
		if off+62 > len(data) {
			return nil, fmt.Errorf("index 截断于第 %d 条", i)
		}
		sha := hex.EncodeToString(data[off+40 : off+60])
		flags := binary.BigEndian.Uint16(data[off+60 : off+62])
		nameLen := int(flags & 0x0FFF)
		p := off + 62
		if nameLen == 0x0FFF { // 超长路径，读到 NUL
			end := bytes.IndexByte(data[p:], 0)
			if end < 0 {
				return nil, fmt.Errorf("index 路径未终止")
			}
			nameLen = end
		}
		if p+nameLen > len(data) {
			return nil, fmt.Errorf("index 路径越界")
		}
		name := string(data[p : p+nameLen])
		out = append(out, [2]string{sha, name})
		entryLen := (62 + nameLen + 8) &^ 7 // 1-8 字节 NUL 填充到 8 的倍数
		off += entryLen
	}
	return out, nil
}

// parseTree 解析 tree object，返回 (mode, sha, name)
func parseTree(payload []byte) [][3]string {
	var out [][3]string
	i := 0
	for i < len(payload) {
		sp := bytes.IndexByte(payload[i:], ' ')
		if sp < 0 {
			break
		}
		mode := string(payload[i : i+sp])
		i += sp + 1
		nul := bytes.IndexByte(payload[i:], 0)
		if nul < 0 || i+nul+21 > len(payload) {
			break
		}
		name := string(payload[i : i+nul])
		sha := hex.EncodeToString(payload[i+nul+1 : i+nul+21])
		i += nul + 21
		out = append(out, [3]string{mode, sha, name})
	}
	return out
}

func safeJoin(root, rel string) (string, bool) {
	rel = filepath.Clean("/" + rel)[1:] // 去掉前导 /，折叠 ..
	if strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.Join(root, rel), true
}

// DumpGit 尝试从暴露的 .git 还原源码树
func DumpGit(base string, client *http.Client, outDir string) *GitDumpResult {
	res := &GitDumpResult{Dir: outDir}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		res.Err = err.Error()
		return res
	}

	// HEAD 信息（用于展示）
	headBytes, _ := httpGet(client, base+"/.git/HEAD")
	var headSHA string
	if headBytes != nil {
		h := strings.TrimSpace(string(headBytes))
		if strings.HasPrefix(h, "ref:") {
			ref := strings.TrimSpace(strings.TrimPrefix(h, "ref:"))
			if b, err := httpGet(client, base+"/"+strings.TrimPrefix(".git/"+ref, "/")); err == nil {
				headSHA = strings.TrimSpace(string(b))
			}
		} else if isHexSHA(h) {
			headSHA = h
		}
	}
	if headSHA != "" {
		res.Commit = headSHA
		if obj, err := fetchGitObject(client, base, headSHA); err == nil && obj.typ == "commit" {
			if idx := bytes.Index(obj.payload, []byte("\n\n")); idx >= 0 {
				res.CommitMsg = strings.TrimSpace(string(obj.payload[idx+2:]))
			}
		}
	}

	// 路径一：.git/index
	if idxBytes, err := httpGet(client, base+"/.git/index"); err == nil {
		if entries, err := parseGitIndex(idxBytes); err == nil && len(entries) > 0 {
			res.Files = fetchBlobs(client, base, entries, outDir)
			if len(res.Files) > 0 {
				return res
			}
		}
	}

	// 路径二：HEAD commit → tree 递归
	if headSHA != "" {
		var files []string
		var walk func(treeSHA, prefix string)
		walk = func(treeSHA, prefix string) {
			obj, err := fetchGitObject(client, base, treeSHA)
			if err != nil || obj.typ != "tree" {
				return
			}
			for _, e := range parseTree(obj.payload) {
				mode, sha, name := e[0], e[1], e[2]
				if mode == "40000" || mode == "040000" {
					walk(sha, prefix+name+"/")
					continue
				}
				if mode == "160000" { // submodule
					continue
				}
				blob, err := fetchGitObject(client, base, sha)
				if err != nil || blob.typ != "blob" {
					continue
				}
				rel := prefix + name
				if writeBlob(outDir, rel, blob.payload) == nil {
					files = append(files, rel)
				}
			}
		}
		if obj, err := fetchGitObject(client, base, headSHA); err == nil && obj.typ == "commit" {
			if m := bytes.Index(obj.payload, []byte("tree ")); m >= 0 && m+45 <= len(obj.payload) {
				walk(string(obj.payload[m+5:m+45]), "")
			}
		}
		res.Files = files
		if len(files) > 0 {
			return res
		}
	}

	res.Err = "无法还原：.git/index 不可用且 loose object 拉取失败（对象可能已打包为 packfile）"
	return res
}

func fetchBlobs(client *http.Client, base string, entries [][2]string, outDir string) []string {
	var mu sync.Mutex
	var files []string
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, e := range entries {
		sha, rel := e[0], e[1]
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			obj, err := fetchGitObject(client, base, sha)
			if err != nil || obj.typ != "blob" {
				return
			}
			if writeBlob(outDir, rel, obj.payload) == nil {
				mu.Lock()
				files = append(files, rel)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return files
}

func writeBlob(root, rel string, data []byte) error {
	full, ok := safeJoin(root, rel)
	if !ok {
		return fmt.Errorf("路径不安全: %s", rel)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, data, 0o644)
}

func httpGet(client *http.Client, url string) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}
