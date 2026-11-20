// 演示站：在本地起三个站点，故意遗留常见敏感文件，用于验证巡检工具。
// site-a(9101): 真实 .git 仓库 + backup.zip + .env
// site-b(9102): .svn + 编辑器临时文件 + phpinfo/debug 页面
// site-c(9103): 干净站点（对照组）
package main

import (
	"archive/zip"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	dir := flag.String("dir", "demo-data", "演示站数据目录")
	flag.Parse()

	if err := buildSiteA(filepath.Join(*dir, "site-a")); err != nil {
		log.Fatalf("构建 site-a 失败: %v", err)
	}
	if err := buildSiteB(filepath.Join(*dir, "site-b")); err != nil {
		log.Fatalf("构建 site-b 失败: %v", err)
	}
	if err := buildSiteC(filepath.Join(*dir, "site-c")); err != nil {
		log.Fatalf("构建 site-c 失败: %v", err)
	}

	serve := func(port string, root string) {
		go func() {
			log.Printf("演示站 http://localhost:%s -> %s", port, root)
			if err := http.ListenAndServe(":"+port, http.FileServer(http.Dir(root))); err != nil {
				log.Fatal(err)
			}
		}()
	}
	serve("9101", filepath.Join(*dir, "site-a"))
	serve("9102", filepath.Join(*dir, "site-b"))
	serve("9103", filepath.Join(*dir, "site-c"))
	select {}
}

func write(root, rel, content string) error {
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(content), 0o644)
}

// site-a：一个“外包交付的 PHP 站”，整个目录（含 .git）被直接上传
func buildSiteA(root string) error {
	if _, err := os.Stat(filepath.Join(root, ".git", "HEAD")); err == nil {
		return nil // 已构建过
	}
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	files := map[string]string{
		"index.html":      "<h1>某某企业官网</h1><p>外包交付 v1.2</p>",
		"index.php":       "<?php\nrequire_once 'config.php';\nrequire_once 'src/db.php';\necho render_home();\n",
		"config.php":      "<?php\n// 数据库配置\ndefine('DB_HOST', '10.0.1.21');\ndefine('DB_USER', 'webapp');\ndefine('DB_PASS', 'W3b@pp#2024!');\ndefine('DB_NAME', 'corp_site');\ndefine('AES_KEY', 'k9f2m8xqp1zv7r4t');\n",
		"src/db.php":      "<?php\nfunction db() { return new PDO('mysql:host='.DB_HOST.';dbname='.DB_NAME, DB_USER, DB_PASS); }\n",
		"src/render.php":  "<?php\nfunction render_home() { return '<h1>home</h1>'; }\n",
		"admin/login.php": "<?php\n// 后台登录，写死的管理员口令（历史遗留）\nif ($_POST['u']==='admin' && $_POST['p']==='admin@888') { session_start(); }\n",
		"README.md":       "# 企业官网\n交付日期 2024-03，联系外包方维护。\n",
	}
	for rel, content := range files {
		if err := write(root, rel, content); err != nil {
			return err
		}
	}
	// 初始化真实 git 仓库（两次提交，让历史也有内容）
	git := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=demo", "GIT_AUTHOR_EMAIL=demo@example.com",
			"GIT_COMMITTER_NAME=demo", "GIT_COMMITTER_EMAIL=demo@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git %v: %s", args, out)
		}
		return nil
	}
	if err := git("init", "-q"); err != nil {
		return err
	}
	if err := git("add", "-A"); err != nil {
		return err
	}
	if err := git("commit", "-q", "-m", "初始交付"); err != nil {
		return err
	}
	if err := write(root, "src/render.php", "<?php\nfunction render_home() { return '<h1>home v2</h1>'; }\n"); err != nil {
		return err
	}
	if err := git("add", "-A"); err != nil {
		return err
	}
	if err := git("commit", "-q", "-m", "首页文案改版"); err != nil {
		return err
	}

	// 运维“图省事”留下的整站备份与 .env
	if err := makeZip(filepath.Join(root, "backup.zip"), files); err != nil {
		return err
	}
	if err := write(root, ".env", "APP_ENV=production\nDB_PASSWORD=W3b@pp#2024!\nSECRET_KEY=k9f2m8xqp1zv7r4t\n"); err != nil {
		return err
	}
	return nil
}

// site-b：.svn 遗留 + 编辑器临时文件 + 调试页
func buildSiteB(root string) error {
	if _, err := os.Stat(filepath.Join(root, "index.html")); err == nil {
		return nil
	}
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	files := map[string]string{
		"index.html":     "<h1>电商平台</h1><p>促销系统</p>",
		".svn/entries":   "12\n\ndir\n9247\nhttp://svn.internal.local/shop/trunk\nhttp://svn.internal.local\n\n\n2024-05-11T08:23:41.000000Z\n",
		".svn/wc.db":     "SQLite format 3\x00 (伪装的 wc.db，真实场景为 sqlite 数据库)",
		"index.php~":     "<?php\n// 首页上一版\n$conn = mysql_connect('db.internal', 'shop', 'Sh0p!2024');\n",
		"index.php.bak":  "<?php\n// 首页备份\n$conn = mysql_connect('db.internal', 'shop', 'Sh0p!2024');\n",
		".index.php.swp": "b0VIM 9.0\x00 (vim 交换文件，含未保存的源码)",
		"config.php~":    "<?php\ndefine('DB_PASS', 'Sh0p!2024');\ndefine('PAY_KEY', 'pk_live_51HxQz...');\n",
		"phpinfo.php":    "<h1>PHP Version 8.1.27</h1><p>System: Linux web-02 ...（phpinfo 输出）</p>",
		"debug/vars":     `{"cmdline": ["php-fpm"], "memstats": {"Alloc": 4194304}, "queue_depth": 3}`,
		"server-status":  "Apache Server Status\nTotal accesses: 88412\nGET /admin/export.php HTTP/1.1\n",
		"test.php":       "<?php phpinfo();",
		".DS_Store":      "Bud1\x00 (macOS 目录元数据)",
	}
	for rel, content := range files {
		if err := write(root, rel, content); err != nil {
			return err
		}
	}
	return nil
}

// site-c：干净站点
func buildSiteC(root string) error {
	if _, err := os.Stat(filepath.Join(root, "index.html")); err == nil {
		return nil
	}
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	return write(root, "index.html", "<h1>干净的站点</h1><p>什么都没留下。</p>")
}

func makeZip(path string, files map[string]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for rel, content := range files {
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return err
		}
	}
	return zw.Close()
}
