# CPA-Auto-Proxy-Test

CLIProxyAPI 插件：自动识别并将不同认证文件的流量路由到可用的代理节点。

针对具有不同出口地区限制的认证文件自动测试可用代理，插件通过 usage 钩子观测请求结果，自动为每个认证文件设置最佳可用代理，写入认证文件的 `proxy_url` 由 CPA 自动热加载生效。

---

## 工作原理

1. **失败观测与智能过滤**：监听请求使用量记录（`usage_plugin`），当匹配到错误状态码或响应体关键词（如 `User location is not supported`）时触发代理切换。留空配置默认自动过滤客户端主动中断的无效错误（如 HTTP 499 / `context canceled`），避免正常业务中断导致误切。
2. **自适应平滑评分（贝叶斯先验与置信度平滑）**：
   - 动态置信度加权：近期成功率权重受近期样本量动态平滑调节（0 到 0.7），无近期请求时自动平滑回退至长期指标，彻底消除闲置节点评分虚高问题。
   - 全局经验先验：自动聚合集群各代理全局观测数据作为经验贝叶斯先验，新凭证未测节点优先调度至全局表现优异的高可用节点，避免全量凭证盲目踩坑。
3. **故障敏捷熔断与避障切换**：
   - 严禁原地死锁：节点故障触发切换时强制排除当前故障节点，保障流量立即故障转移（Failover）。
   - 指数退避冷却：节点发生故障时自动触发冷却倒计时（默认 15s 起步，随连续故障呈指数递增），冷却期内优先将流量调度至健康节点。
   - 切换防抖与在途失败隔离：为每个凭证维护切换防抖窗口（默认 10 秒）。切换代理后在此时间窗口内压制凭证频繁二次切换，并将延迟到达的在途并发失败正确归因于原代理，防止误扣新代理评分并彻底杜绝并发故障下的循环刷盘风暴。
   - 僵尸凭证限频：对全节点彻底不可用的失效凭证自动限频写盘，保护宿主磁盘 I/O 与文件监听器免受刷盘风暴影响。
4. **热更新生效与内存自愈**：仅修改对应凭证 JSON 文件的 `proxy_url` 字段（原子写，不破坏 token 刷新），CPA watcher 自动热加载生效；纯内存维护评分与状态，正常请求零磁盘 I/O；启动与配置重载时自动从凭证文件中清理已废弃或移除的坏代理。

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
      min-recent-samples: 5
      failure-cooldown-sec: 15
      switch-debounce-sec: 10
```

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `auth-type` | string | `""` | 受管凭证类型（不区分大小写，支持逗号分隔配置多个类型） |
| `proxies` | string[] | `[]` | 可选的上游代理节点 URL 列表 |
| `failure-patterns` | string[] | `[]` | 触发切换的失败规则（正则匹配响应体或纯数字状态码；留空匹配全部失败并自动忽略 499 客户端中断） |
| `window-hours` | int | `24` | 近期成功率的滑动统计窗口（小时） |
| `min-recent-samples` | int | `5` | 近期窗口置信度最小样本量，低于此值平滑结合长期指标与全局经验先验 |
| `failure-cooldown-sec` | int | `15` | 故障节点基础冷却时长（秒），连续故障时指数递增退避 |
| `switch-debounce-sec` | int | `10` | 账号级切换防抖与在途失败隔离窗口（秒），在此窗口内压制二次切换并隔离在途失败 |

---

## 管理面板 (Web UI)

插件自带管理页面，遵循 CPA 资源路由安全边界规范（`/v0/resource/plugins/...` 仅提供纯静态部署资源）：
- **Web UI 静态页面外壳**：
  - CPA 管理中心菜单：可在 CPA 管理面板插件列表中直接点击进入
  - 静态资源路径：`GET /v0/resource/plugins/cpa-auto-proxy-test/page`（纯静态原生页面外壳，不暴露任何运行时凭据或状态）
- **受保护管理端点 (Protected Management APIs)**：
  - **状态快照**：`GET /v0/management/cpa-auto-proxy-test/status`（受 CPA Management Key 保护）
  - **数据导出**：`GET /v0/management/cpa-auto-proxy-test/export`（受 CPA Management Key 保护）
- **同源自动鉴权机制**：
  - 前端静态页面遵循 CPA 插件商店同源规范，自动从浏览器存储（如 `management_key`）优先提取明文管理密钥，携带 `Authorization: Bearer <key>` 与 `X-Management-Key: <key>` 调用管理端点。
  - 严格排除内部加密格式（如 `enc::` 前缀），零配置即开即用，无需任何手动输入或密钥配置组件。
- **功能特性**：
  - 一键导出完整诊断数据 JSON（包含最近失败详情与节点切换轨迹）
  - 各认证文件的当前生效代理与近期/累计请求统计
  - 候选代理全局汇总统计（成功/失败率高亮徽标）
  - 点击折叠行查看各凭证在候选代理上的平滑分值、成功率对比及最后观测时间
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
go run build.go -version 0.2.1              # 指定 Release 版本号（默认 0.2.1）
go run build.go -target windows/amd64        # 单独构建指定目标平台（支持 windows/amd64, windows/arm64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64）
go run build.go -target darwin/arm64 -keep  # 单独构建并保留 build/ 目录下已有产物
go run build.go -no-wsl                      # 禁用 Windows 下自动回退到 WSL 构建
```

### 安装与发布

- **发布到 GitHub Release**：直接将 `build/` 下的 `.zip` 压缩包（如 `cpa-auto-proxy-test_0.2.1_linux_amd64.zip`）和 `checksums.txt` 上传至 GitHub Release 附件即可。
- **手动安装**：解压对应平台的动态库文件（Windows: `cpa-auto-proxy-test.dll`、Linux: `cpa-auto-proxy-test.so`、macOS: `cpa-auto-proxy-test.dylib`）放入 CPA 的 `plugins/` 目录，配置好 CPA 主配置中的插件段即可。
