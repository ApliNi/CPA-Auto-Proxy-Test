# CPA-Auto-Proxy-Test

CLIProxyAPI 插件：自动识别并将不同认证文件的流量路由到可用的代理节点。

针对 Antigravity 等凭证对出口地区限制敏感（如 `User location is not supported`）的问题，插件通过 usage 钩子观测真实请求结果，自动学习并为每个认证文件匹配最佳可用代理，写入认证文件的 `proxy_url` 由 CPA 自动热加载生效。

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
    CPA-Auto-Proxy-Test:
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
| `auth-type` | string | `Antigravity` | 受管凭证类型（不区分大小写） |
| `proxies` | string[] | `[]` | 可选的上游代理节点 URL 列表 |
| `failure-patterns` | string[] | `[]` | 触发切换的失败规则（正则匹配响应体或纯数字状态码；留空匹配全部失败） |
| `window-hours` | int | `24` | 近期成功率的滑动统计窗口（小时） |

---

## 管理面板 (Web UI)

插件自带管理页面，启动后可在 CPA 管理面板菜单中访问：
- **Web UI 页面**：`http://<cpa-host>:<cpa-port>/management.html#/plugin-pages/CPA-Auto-Proxy-Test`
- **直接访问**：`GET /v0/resource/plugins/CPA-Auto-Proxy-Test/page`
- **数据导出**：支持在界面一键下载诊断数据 JSON（包含最近失败详情与节点切换轨迹）。

界面提供：
- 各认证文件的当前生效代理
- 每个凭证的请求总计成功/失败次数（高亮异常文件）
- 点击展开查看各候选代理的近期/累计成功率、平滑分值及最后观测时间

---

## 构建与安装

### 一键全平台构建 (Windows / WSL)

在仓库根目录下运行：

```bash
go run build.go
```

构建脚本会自动编译各平台动态库，并按 **CPA 插件市场规范** 自动打包装入 `build/` 目录：
- `CPA-Auto-Proxy-Test_0.1.0_windows_amd64.zip`（内含根目录 `CPA-Auto-Proxy-Test.dll`）
- `CPA-Auto-Proxy-Test_0.1.0_windows_arm64.zip`（内含根目录 `CPA-Auto-Proxy-Test.dll`）
- `CPA-Auto-Proxy-Test_0.1.0_linux_amd64.zip`（内含根目录 `CPA-Auto-Proxy-Test.so`）
- `CPA-Auto-Proxy-Test_0.1.0_linux_arm64.zip`（内含根目录 `CPA-Auto-Proxy-Test.so`）
- `checksums.txt`（包含所有产物的标准 SHA256 校验和）

支持的参数：
```bash
go run build.go -version 0.1.0       # 指定 Release 版本号（默认 0.1.0）
go run build.go -target windows/amd64 # 单独构建目标平台
```

### 安装与发布

- **发布到 GitHub Release**：直接将 `build/` 下的 `.zip` 压缩包和 `checksums.txt` 上传至 GitHub Release 附件即可。
- **手动安装**：解压对应平台的动态库文件（`.dll` 或 `.so`）放入 CPA 的 `plugins/` 目录，配置好 CPA 主配置中的插件段即可。
