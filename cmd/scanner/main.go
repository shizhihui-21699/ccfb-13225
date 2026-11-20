package main

import (
	"embed"
	"flag"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"leftoverscan/internal/scan"
)

//go:embed all:web
var webFS embed.FS

var funcMap = template.FuncMap{
	"sevClass": func(s string) string {
		switch s {
		case "严重":
			return "sev-crit"
		case "高":
			return "sev-high"
		case "中":
			return "sev-mid"
		default:
			return "sev-low"
		}
	},
	"confClass": func(c string) string {
		if c == "高" {
			return "conf-high"
		}
		if c == "中" {
			return "conf-mid"
		}
		return "conf-low"
	},
}

var (
	tplIndex  *template.Template
	tplCard   *template.Template
	tplResult *template.Template
	mgr       *scan.Manager
)

func main() {
	addr := flag.String("addr", ":8080", "监听地址")
	dataDir := flag.String("data", "data", "git 还原输出目录")
	flag.Parse()

	var err error
	tplIndex, err = template.New("index.html").Funcs(funcMap).ParseFS(webFS, "web/templates/index.html", "web/templates/card.html", "web/templates/result.html")
	if err != nil {
		log.Fatal(err)
	}
	tplCard = tplIndex.Lookup("card")
	tplResult = tplIndex.Lookup("result")

	mgr = scan.NewManager(*dataDir)

	staticFS, err := fs.Sub(webFS, "web/static")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))
	mux.HandleFunc("GET /", handleIndex)
	mux.HandleFunc("POST /sites", handleAddSite)
	mux.HandleFunc("POST /sites/{id}/scan", handleScan)
	mux.HandleFunc("GET /sites/{id}/result", handleResult)

	log.Printf("巡检台监听于 http://localhost%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if err := tplIndex.ExecuteTemplate(w, "index.html", map[string]any{"Sites": mgr.Sites()}); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func handleAddSite(w http.ResponseWriter, r *http.Request) {
	raw := r.FormValue("url")
	s, err := mgr.AddSite(raw)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	// 添加后立即开始扫描
	_ = mgr.StartScan(s.ID)
	if err := tplCard.ExecuteTemplate(w, "card", s); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func handleScan(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := mgr.StartScan(id); err != nil && !strings.Contains(err.Error(), "正在扫描") {
		http.Error(w, err.Error(), 400)
		return
	}
	s := mgr.Get(id)
	if err := tplResult.ExecuteTemplate(w, "result", s); err != nil {
		http.Error(w, err.Error(), 500)
	}
}

func handleResult(w http.ResponseWriter, r *http.Request) {
	s := mgr.Get(r.PathValue("id"))
	if s == nil {
		http.Error(w, "站点不存在", 404)
		return
	}
	if err := tplResult.ExecuteTemplate(w, "result", s); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
