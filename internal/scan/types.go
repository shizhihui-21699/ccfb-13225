package scan

import "time"

// Category 遗留文件分类
type Category string

const (
	CatBackup Category = "备份文件"
	CatVCS    Category = "版本库泄露"
	CatEditor Category = "编辑器临时文件"
	CatDebug  Category = "调试接口/页面"
	CatConfig Category = "敏感配置"
)

// Probe 字典里的一条探测项
type Probe struct {
	Path     string
	Category Category
	Severity string // 严重/高/中/低
	Note     string
}

// Finding 一条命中结果
type Finding struct {
	Path       string
	URL        string
	Category   Category
	Severity   string
	Note       string
	Status     int
	Size       int64
	Confidence string // 高/中/低
}

// GitDumpResult git 还原结果
type GitDumpResult struct {
	Dir       string   // 还原输出目录
	Files     []string // 还原出的文件列表
	Commit    string   // HEAD commit 摘要
	CommitMsg string
	Err       string
}

// Site 一个被巡检站点
type Site struct {
	ID       string
	URL      string
	Status   string // pending / scanning / done / error
	Findings []Finding
	Git      *GitDumpResult
	Started  time.Time
	Finished time.Time
	Err      string
}

func (s *Site) Running() bool { return s.Status == "scanning" || s.Status == "pending" }

// CountByCategory 统计各类命中数
func (s *Site) CountByCategory(c Category) int {
	n := 0
	for _, f := range s.Findings {
		if f.Category == c {
			n++
		}
	}
	return n
}
