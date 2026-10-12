# CPA-Auto-Proxy-Test

CLIProxyAPI 插件：自动识别并将不同认证文件的流量路由到可用的代理节点。

针对具有不同出口地区限制的认证文件自动测试可用代理，插件通过 usage 钩子观测请求结果，自动为每个认证文件设置最佳可用代理，写入认证文件的 `proxy_url` 由 CPA 自动热加载生效。

---

## 工作原理

1. **失败观测**：监听请求使用量记录（`usage_plugin`），当匹配到错误状态码或响应体关键词（如 `User location is not supported`）时触发代理切换。
2. **平滑评分**：综合 24 小时近期成功率（权重 0.7）与长期成功率（权重 0.3），采用拉普拉斯平滑评分。优先尝试未测节点，全节点故障时自适应轮询重试。
3. **热更新生效**：仅修改对应凭证 JSON 文件的 `proxy_url` 字段（原子写，不破坏 token 刷新），CPA watcher 自动热加载生效。
4. **高性能与自愈**：纯内存维护评分与状态，正常请求零磁盘 I/O；启动与配置重载时自动从凭证文件中清理已废弃或移除的坏代理。

---

## 插件配置

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    cpa-auto-proxy-test:
      enabled: true
      priority: 10
      auth-type: "Antigravity"
      proxies:
        - "http://127.0.0.1:7890"
        - "socks5://127.0.0.1:7891"
      failure-patterns:
        - "User location is not supported for the API use"
        - "400"
      window-hours: 24
```

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `auth-type` | string | `""` | 受管凭证类型（不区分大小写，支持逗号分隔配置多个类型） |
| `proxies` | string[] | `[]` | 可选的上游代理节点 URL 列表 |
| `failure-patterns` | string[] | `[]` | 触发切换的失败规则（正则匹配响应体或纯数字状态码；留空匹配全部失败） |
| `window-hours` | int | `24` | 近期成功率的滑动统计窗口（小时） |

---

## 管理面板 (Web UI)

插件自带管理页面，启动后可在 CPA 管理面板菜单中访问：
- **Web UI 页面**：
  - CPA 管理中心菜单：可在 CPA 管理面板菜单列表中直接点击进入
  - 独立免密直接访问：`GET /v0/resource/plugins/cpa-auto-proxy-test/page`（纯静态原生页面，无需任何鉴权密钥）
- **数据与导出端点**：
  - **状态快照**：`GET /v0/resource/plugins/cpa-auto-proxy-test/status`
  - **数据导出**：`GET /v0/resource/plugins/cpa-auto-proxy-test/export`
- **数据导出**：支持在界面一键下载诊断数据 JSON（包含最近失败详情与节点切换轨迹）。
- **完全免密交互**：界面采用纯原生免密独立架构，直接通过同源资源端点实时拉取状态和导出诊断数据，无需配置或依赖任何 Management Key。
界面提供：
- 各认证文件的当前生效代理
- 每个凭证的请求近期与累计成功/失败次数（高亮异常文件）
- 候选代理汇总统计（各代理在所有凭证下的全局近期与累计成功/失败次数）
- 点击展开查看各候选代理的近期/累计成功率、平滑分值及最后观测时间
---
## 构建与安装

本插件支持跨平台通用原生构建，可在 Windows、Linux、macOS 等任意操作系统上直接运行，无操作系统限制。构建脚本采用纯 Go 标准库实现压缩打包（ZIP）与 SHA-256 校验和计算，无需额外安装 Python、zip 或 sha256sum 等外部工具。

### 编译器方案

由于插件采用 CGO（`-buildmode=c-shared`）编译动态链接库，构建过程需要 C 编译器：

- **推荐方案（Zig 零配置全目标交叉编译）**：
  在任意操作系统上本地安装 [Zig](https://ziglang.org/)（例如 Windows 使用 `winget install zig` 或 `scoop install zig`，macOS 使用 `brew install zig`，Linux 使用系统包管理器或直接解压安装）。`build.go` 会自动检测并调用 `zig cc`，零配置即可在任意平台上一次性交叉编译全部目标平台（Windows / Linux / macOS 的 amd64 与 arm64）。
- **本机编译器编译本机目标**：
  若仅需编译当前操作系统对应架构的产物，可直接使用本机已安装的 GCC 或 Clang（例如：`go run build.go -target windows/amd64`）。
- **Windows 动态无缝回退 WSL**：
  在 Windows 环境下若未安装本地 C 编译器（Zig / GCC），脚本会自动检测并无缝回退到 WSL 环境完成构建；若不需要回退，可使用 `-no-wsl` 参数禁用回退。

### 构建命令

在仓库根目录下运行：

```bash
# 一键交叉编译全部目标平台产物并生成校验和
go run build.go

# 常用参数：
go run build.go -version 0.1.0              # 指定 Release 版本号（默认 0.1.0）
go run build.go -target windows/amd64        # 单独构建指定目标平台（支持 windows/amd64, windows/arm64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64）
go run build.go -target darwin/arm64 -keep  # 单独构建并保留 build/ 目录下已有产物
go run build.go -no-wsl                      # 禁用 Windows 下自动回退到 WSL 构建
```

### 安装与发布

- **发布到 GitHub Release**：直接将 `build/` 下的 `.zip` 压缩包（如 `cpa-auto-proxy-test_0.1.0_linux_amd64.zip`）和 `checksums.txt` 上传至 GitHub Release 附件即可。
- **手动安装**：解压对应平台的动态库文件（Windows: `cpa-auto-proxy-test.dll`、Linux: `cpa-auto-proxy-test.so`、macOS: `cpa-auto-proxy-test.dylib`）放入 CPA 的 `plugins/` 目录，配置好 CPA 主配置中的插件段即可。
