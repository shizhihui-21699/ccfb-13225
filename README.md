# 站点遗留文件巡检（leftover-scan）

针对外包交付站点的遗留文件巡检工具：按常见命名探测备份压缩包、暴露的版本库目录、
编辑器临时文件、敏感配置与调试接口；发现可下载的 `.git` 时直接还原源码目录结构以证明危害。
Go + HTMX，无外部依赖。

## 运行

```bash
# 1. 启动演示站（9101/9102/9103）
go run ./cmd/demosite

# 2. 启动巡检台
go run ./cmd/scanner -addr :8080 -data data
```

打开 http://localhost:8080 ，输入站点地址（如 `http://localhost:9101`）即添加并扫描，
结果按站点卡片实时刷新（HTMX 轮询，扫描完成自动停止）。

## 演示站

| 端口 | 内容 |
|---|---|
| 9101 | 真实 `.git` 仓库（两次提交）+ `backup.zip` + `.env` |
| 9102 | `.svn/entries`、`wc.db`、编辑器临时文件（`index.php~`、`.index.php.swp`、`config.php~`）、`phpinfo.php`、`debug/vars`、`server-status` |
| 9103 | 干净站点，对照组 |

## 工作原理

- **基线去误报**：先请求一个必然不存在的路径，识别“全站 200”的兜底路由；
  命中项与基线响应大小接近时自动降置信度。
- **内容级校验**：`.git/HEAD` 必须以 `ref:` 开头或为 40 位 SHA；`.env` 必须含 `=`。
- **路径转义**：探测路径逐段 URL 转义（`#index.php#` 这类 emacs 临时文件不转义会被当成 fragment）。
- **git 还原**（`internal/scan/gitdump.go`）：优先解析 `.git/index` 拿到工作区文件清单与 blob SHA，
  逐个拉取 loose object 解压写盘；index 不可用时退化为 `HEAD → commit → tree` 递归遍历。
  仅支持 loose object，packfile 暂不支持（会明确报出原因）。

## 探测字典

`internal/scan/wordlist.go`，初始约 90 条，分五类：

- **备份文件**：`backup/www/wwwroot/site/htdocs.{zip,tar.gz,rar,7z}`、`db/dump/mysql.sql` 等
- **版本库**：`.git/HEAD|config|index`、`.svn/entries|wc.db`、`.hg`、`.bzr`、`CVS`
- **编辑器临时文件**：`index.php~|.bak|.swp|.swo|.orig`、`#index.php#`、`wp-config.php.bak` 等
- **敏感配置**：`.env*`、`web.config`、`.htpasswd`、`database.yml`、`.npmrc`、docker 凭据等
- **调试接口**：`phpinfo.php`、`server-status`、`debug/vars`、`debug/pprof`、`actuator/*`、`elmah.axd`、`trace.axd` 等

## 注意

仅用于对**自有或获授权**站点的安全巡检。还原出的源码写入 `-data` 目录，注意妥善保管与清理。
