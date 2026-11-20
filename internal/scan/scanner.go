package scan

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Manager 管理站点与扫描任务
type Manager struct {
	mu      sync.Mutex
	sites   map[string]*Site
	order   []string
	client  *http.Client
	dataDir string // git 还原输出根目录
}

func NewManager(dataDir string) *Manager {
	return &Manager{
		sites:   map[string]*Site{},
		dataDir: dataDir,
		client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // 不跟随跳转，避免 302 到首页被误判为命中
			},
		},
	}
}

func (m *Manager) AddSite(raw string) (*Site, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("站点地址不能为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("地址无法解析: %s", raw)
	}
	id := randomID()
	s := &Site{ID: id, URL: strings.TrimRight(u.String(), "/"), Status: "pending"}
	m.mu.Lock()
	m.sites[id] = s
	m.order = append([]string{id}, m.order...)
	m.mu.Unlock()
	return s, nil
}

func (m *Manager) Sites() []*Site {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Site, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.sites[id])
	}
	return out
}

func (m *Manager) Get(id string) *Site {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sites[id]
}

// StartScan 异步启动一次扫描
func (m *Manager) StartScan(id string) error {
	s := m.Get(id)
	if s == nil {
		return fmt.Errorf("站点不存在")
	}
	m.mu.Lock()
	if s.Status == "scanning" {
		m.mu.Unlock()
		return fmt.Errorf("该站点正在扫描中")
	}
	s.Status = "scanning"
	s.Findings = nil
	s.Git = nil
	s.Err = ""
	s.Started = time.Now()
	m.mu.Unlock()

	go m.scanSite(s)
	return nil
}

type fetchResult struct {
	status int
	body   []byte
}

func (m *Manager) fetch(rawURL string) (*fetchResult, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; leftover-scan/1.0)")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 最多读 8MB，防止大包拖垮
	if err != nil {
		return nil, err
	}
	return &fetchResult{status: resp.StatusCode, body: body}, nil
}

func (m *Manager) scanSite(s *Site) {
	defer func() {
		m.mu.Lock()
		if s.Status == "scanning" {
			s.Status = "done"
		}
		s.Finished = time.Now()
		m.mu.Unlock()
	}()

	// 基线：请求一个必然不存在的路径，识别“全站 200”的伪静态/兜底路由
	base := s.URL
	baseline, berr := m.fetch(base + "/leftover-scan-" + randomID() + ".notexist")
	baseStatus, baseSize := 0, int64(-1)
	if berr == nil {
		baseStatus = baseline.status
		baseSize = int64(len(baseline.body))
	}

	var mu sync.Mutex
	var findings []Finding
	gitExposed := false

	sem := make(chan struct{}, 12)
	var wg sync.WaitGroup
	for _, p := range Wordlist {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			full := base + "/" + escapeProbePath(p.Path)
			res, err := m.fetch(full)
			if err != nil {
				return
			}
			if res.status != http.StatusOK {
				// .git 被 403 也说明目录存在，值得记录
				if res.status == http.StatusForbidden && strings.HasPrefix(p.Path, ".git") {
					mu.Lock()
					findings = append(findings, Finding{
						Path: p.Path, URL: full, Category: p.Category,
						Severity: "中", Note: p.Note + "（403，目录存在但被拒绝，可能可绕过）",
						Status: res.status, Confidence: "中",
					})
					mu.Unlock()
				}
				return
			}
			size := int64(len(res.body))
			conf := "高"
			if baseStatus == http.StatusOK {
				// 与基线大小接近 → 大概率是兜底页面，降置信度
				diff := size - baseSize
				if diff < 0 {
					diff = -diff
				}
				if baseSize >= 0 && diff <= baseSize/10+64 {
					conf = "低"
				} else {
					conf = "中"
				}
			}
			// 内容级校验，进一步确认
			if p.Path == ".git/HEAD" {
				b := strings.TrimSpace(string(res.body))
				if !strings.HasPrefix(b, "ref:") && !isHexSHA(b) {
					return // 不是真的 git HEAD
				}
				conf = "高"
			}
			if p.Path == ".env" && !strings.Contains(string(res.body), "=") {
				return
			}
			mu.Lock()
			findings = append(findings, Finding{
				Path: p.Path, URL: full, Category: p.Category,
				Severity: p.Severity, Note: p.Note,
				Status: res.status, Size: size, Confidence: conf,
			})
			if p.Path == ".git/HEAD" && conf == "高" {
				gitExposed = true
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	sort.SliceStable(findings, func(i, j int) bool {
		return severityRank(findings[i].Severity) > severityRank(findings[j].Severity)
	})

	m.mu.Lock()
	s.Findings = findings
	m.mu.Unlock()

	// git 泄露 → 直接尝试还原源码目录结构
	if gitExposed {
		res := DumpGit(base, m.client, m.dataDir+"/"+s.ID)
		m.mu.Lock()
		s.Git = res
		m.mu.Unlock()
	}
}

func severityRank(s string) int {
	switch s {
	case "严重":
		return 4
	case "高":
		return 3
	case "中":
		return 2
	default:
		return 1
	}
}

func randomID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func isHexSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// escapeProbePath 逐段转义探测路径，防止 #、? 等字符被当成 URL 分隔符
// （例如 emacs 临时文件 #index.php# 不转义会被当作 fragment，请求实际打到 / 上）
func escapeProbePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}
