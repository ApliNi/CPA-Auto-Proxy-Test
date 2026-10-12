// Package plugin provides the HTML status UI and Management API response builders
// for cpa-auto-proxy-test.
//
// The status page is served as a self-contained, passive HTML/JS shell with zero
// external dependencies. Sensitive runtime metrics and diagnostic exports are
// fetched via standard plugin resource routes without requiring Management Key
// authentication:
//   - GET /v0/resource/plugins/cpa-auto-proxy-test/page
//   - GET /v0/resource/plugins/cpa-auto-proxy-test/status
//   - GET /v0/resource/plugins/cpa-auto-proxy-test/export
package plugin

// StatusPageHTML renders the static HTML status page shell. Runtime account
// metrics and diagnostic records are dynamically fetched by client-side script
// from plugin resource endpoints. Auto-refresh preserves expanded rows by auth ID.
func StatusPageHTML() string {
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>CPA-Auto-Proxy-Test 状态</title>
<style>
:root {
  color-scheme: light dark;
  /* CLI Proxy API Management Center theme tokens (warm gray palette). */
  --bg-secondary: #faf9f5;
  --bg-primary: #f0eee8;
  --bg-tertiary: #e9e6df;
  --floating-surface: #fffdf9;
  --text-primary: #2d2a26;
  --text-secondary: #6d6760;
  --text-tertiary: #a29c95;
  --border-color: #e3e1db;
  --border-primary: #d5d2cb;
  --primary-color: #8b8680;
  --primary-hover: #7f7a74;
  --primary-contrast: #ffffff;
  --success-color: #10b981;
  --error-color: #c65746;
  --success-badge-bg: #d1fae5;
  --success-badge-text: #065f46;
  --failure-badge-bg: rgba(198, 87, 70, 0.14);
  --failure-badge-text: #8a3a30;
  --shadow: 0 1px 2px 0 rgb(0 0 0 / 0.08);
  --radius-md: 8px;
}
@media (prefers-color-scheme: dark) {
  :root {
    --bg-secondary: #151412;
    --bg-primary: #1d1b18;
    --bg-tertiary: #262320;
    --floating-surface: #2a2723;
    --text-primary: #f6f4f1;
    --text-secondary: #c9c3bb;
    --text-tertiary: #9c958d;
    --border-color: #3a3530;
    --border-primary: #4a453f;
    --primary-color: #8b8680;
    --primary-hover: #9e9891;
    --primary-contrast: #151412;
    --success-color: #34d399;
    --error-color: #e06c58;
    --success-badge-bg: rgba(52, 211, 153, 0.18);
    --success-badge-text: #6ee7b7;
    --failure-badge-bg: rgba(224, 108, 88, 0.18);
    --failure-badge-text: #fca5a5;
    --shadow: 0 1px 2px 0 rgb(0 0 0 / 0.35);
  }
}
* { box-sizing: border-box; }
body {
  font-family: system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", sans-serif;
  margin: 0; padding: 20px 24px 40px; background: var(--bg-secondary); color: var(--text-primary);
  margin-inline: auto; line-height: 1.45;
}
header {
  display: flex; flex-wrap: wrap; align-items: baseline;
  gap: 12px; margin-bottom: 16px; padding-bottom: 12px; border-bottom: 1px solid var(--border-color);
}
h1 { font-size: 1.15rem; margin: 0; font-weight: 600; }
#generated { color: var(--text-tertiary); font-size: .84rem; }
.link-btn {
  background: none; border: none; padding: 0; color: var(--primary-hover);
  cursor: pointer; font: inherit; font-size: .84rem;
}
.link-btn:hover { color: var(--text-primary); }
.link-btn:disabled { opacity: .5; cursor: not-allowed; }
#config { background: var(--bg-primary); border: 1px solid var(--border-color);
          border-radius: var(--radius-md); padding: 12px 16px; margin-bottom: 16px;
          font-size: .86rem; line-height: 1.7; box-shadow: var(--shadow); }
#config code { background: var(--bg-tertiary); padding: 1px 6px; border-radius: 4px;
               font-family: ui-monospace, Consolas, monospace; font-size: .82rem; }
.proxy-item { display: inline-flex; align-items: baseline; gap: 4px; margin-right: 12px; margin-bottom: 4px; white-space: nowrap; }
table { border-collapse: collapse; width: 100%; background: var(--bg-primary);
        border: 1px solid var(--border-color); border-radius: var(--radius-md);
        overflow: hidden; font-size: .86rem; box-shadow: var(--shadow); }
th, td { text-align: left; padding: 8px 12px; border-bottom: 1px solid var(--border-color);
         vertical-align: top; }
th { background: var(--bg-tertiary); font-weight: 600; white-space: nowrap;
     color: var(--text-secondary); font-size: .82rem; }
tr.account { cursor: pointer; }
tr.account:hover { background: var(--bg-tertiary); }
tr.detail td { background: var(--floating-surface); padding: 0 0 8px 12px; }
tr.detail td.spacer { width: 90px; }
.detail table { border: none; box-shadow: none; background: transparent; }
.detail th { padding: 6px 12px; }
.detail td { border-bottom: 1px solid var(--border-color); padding: 6px 12px; }
tr.detail tr:last-child td { border-bottom: none; }
code { font-family: ui-monospace, Consolas, monospace; font-size: .82rem; }
.scorebar { display: inline-block; width: 80px; height: 6px; background: var(--bg-tertiary);
            border-radius: 3px; overflow: hidden; vertical-align: middle; margin: 0 6px; }
.scorebar > span { display: block; height: 100%; background: var(--success-color); }
.badge, .muted { display: inline-block; padding: 0 4px; border-radius: 4px; font-size: .74rem; }
.badge.ok { background: var(--success-badge-bg); color: var(--success-badge-text); }
.badge.fail { background: var(--failure-badge-bg); color: var(--failure-badge-text); }
.muted { color: var(--text-tertiary); }
.col-stats { width: 1%; white-space: nowrap; }
#error { color: var(--error-color); margin: 8px 0; font-size: .86rem; }
#empty { color: var(--text-tertiary); margin: 16px 0; }
</style>
</head>
<body>
<header>
  <h1>CPA-Auto-Proxy-Test 代理路由状态</h1>
  <span id="generated"></span>
</header>
<div id="error"></div>
<div id="config"></div>
<div id="empty" hidden>暂无受管账号</div>
<table id="tbl" hidden>
  <thead><tr>
    <th style="width:90px">类型</th><th>认证文件</th><th>当前代理</th><th class="col-stats">总计请求</th>
  </tr></thead>
  <tbody id="rows"></tbody>
</table>
<script>
const REFRESH_MS = 10000;
const expanded = new Set();

function resolveUrl(endpoint) {
  const p = window.location.pathname || '';
  if (p.includes('/resource/plugins/cpa-auto-proxy-test')) {
    const base = p.replace(/\/(page|index\.html|status|export)(\/.*)?$/, '');
    return (base || '/v0/resource/plugins/cpa-auto-proxy-test') + '/' + endpoint;
  }
  return '/v0/resource/plugins/cpa-auto-proxy-test/' + endpoint;
}

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, c => (
    {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
}

function fmtTime(ts, ok) {
  if (!ts) return '<span class="muted">—</span>';
  const d = new Date(ts * 1000);
  return esc(d.toLocaleString()) + (ok
    ? ' <span class="badge ok">成功</span>' : ' <span class="badge fail">失败</span>');
}

function fmtStat(cnt, label, isOk) {
  if (cnt >= 1) {
    return '<span class="badge ' + (isOk ? 'ok' : 'fail') + '">' + cnt + ' ' + label + '</span>';
  }
  return '<span class="muted">0 ' + label + '</span>';
}

function detailTable(a) {
  let rows = '';
  for (const o of (a.observations || [])) {
    const pct = Math.round((o.score || 0) * 100);
    rows += '<tr>' +
      '<td><code>' + esc(o.proxy) + '</code></td>' +
      '<td>' + o.recent_success + ' / ' + o.recent_total + '</td>' +
      '<td>' + o.long_success + ' / ' + o.long_total + '</td>' +
      '<td><span class="scorebar"><span style="width:' + pct + '%"></span></span>' + pct + '%</td>' +
      '<td>' + fmtTime(o.last_seen, o.last_ok) + '</td>' +
      '</tr>';
  }
  if (!rows) return '<span class="muted">无观测记录</span>';
  return '<table><thead><tr>' +
    '<th>代理</th><th>近期 成功/总数</th><th>累计 成功/总数</th><th>分数</th><th>最后观测</th>' +
    '</tr></thead><tbody>' + rows + '</tbody></table>';
}

function render(data) {
  document.getElementById('generated').innerHTML =
    '生成于 ' + esc(data.generated_at || '') +
    ' · <button type="button" id="export-btn" class="link-btn" onclick="downloadExport()">导出数据</button>';
  const cfg = data.config || {};
  const authTypeDisplay = (cfg.auth_type && cfg.auth_type.trim())
    ? '<code>' + esc(cfg.auth_type) + '</code>'
    : '<code>未配置 (不管理任何凭证)</code>';
  const proxyMap = {};
  if (Array.isArray(data.proxy_summaries)) {
    for (const ps of data.proxy_summaries) {
      if (ps && ps.proxy) {
        proxyMap[ps.proxy] = {
          total_success: ps.total_success || 0,
          total_failed: ps.total_failed || 0,
        };
      }
    }
  } else if (Array.isArray(data.accounts)) {
    for (const a of data.accounts) {
      for (const o of (a.observations || [])) {
        if (!proxyMap[o.proxy]) {
          proxyMap[o.proxy] = { total_success: 0, total_failed: 0 };
        }
        proxyMap[o.proxy].total_success += (o.long_success || 0);
        proxyMap[o.proxy].total_failed += Math.max(0, (o.long_total || 0) - (o.long_success || 0));
      }
    }
  }
  const proxyItems = (cfg.proxies || []).length
    ? (cfg.proxies || []).map(p => {
        const stat = proxyMap[p] || { total_success: 0, total_failed: 0 };
        const totOk = stat.total_success || 0;
        const totFail = stat.total_failed || 0;
        return '<span class="proxy-item"><code>' + esc(p) + '</code> ' +
          fmtStat(totOk, '成功', true) + ' ' +
          fmtStat(totFail, '失败', false) + '</span>';
      }).join(' ')
    : '<span class="muted">无</span>';
  document.getElementById('config').innerHTML =
    '认证类型: ' + authTypeDisplay + ' · ' +
    '窗口: <code>' + esc(cfg.window_hours) + ' 小时</code> · ' +
    '失败匹配规则: <code>' + esc(cfg.failure_patterns_count) + '</code> 条<br>' +
    '代理列表: ' + proxyItems;
  const accounts = data.accounts || [];
  document.getElementById('empty').hidden = accounts.length > 0;
  document.getElementById('tbl').hidden = accounts.length === 0;
  const tbody = document.getElementById('rows');
  tbody.innerHTML = '';
  for (const a of accounts) {
    const recOk = a.recent_success || 0;
    const recFail = a.recent_failed || 0;
    const totOk = a.total_success || 0;
    const totFail = a.total_failed || 0;
    const reqStats = '<span class="muted">近期:</span> ' +
      fmtStat(recOk, '成功', true) + ' ' + fmtStat(recFail, '失败', false) +
      ' · <span class="muted">总计:</span> ' +
      fmtStat(totOk, '成功', true) + ' ' + fmtStat(totFail, '失败', false);

    const tr = document.createElement('tr');
    tr.className = 'account';
    tr.innerHTML = '<td>' + esc(a.type) + '</td>' +
      '<td><code>' + esc(a.auth_id) + '</code></td>' +
      '<td><code>' + esc(a.current_proxy || 'default') + '</code></td>' +
      '<td class="col-stats">' + reqStats + '</td>';
    const detail = document.createElement('tr');
    detail.className = 'detail';
    detail.hidden = !expanded.has(a.auth_id);
    detail.innerHTML = '<td class="spacer"></td>' +
      '<td colspan="3">' + detailTable(a) + '</td>';
    tr.addEventListener('click', () => {
      if (expanded.has(a.auth_id)) expanded.delete(a.auth_id);
      else expanded.add(a.auth_id);
      detail.hidden = !expanded.has(a.auth_id);
    });
    tbody.appendChild(tr);
    tbody.appendChild(detail);
  }
}

async function load() {
  const errorEl = document.getElementById('error');
  try {
    const resp = await fetch(resolveUrl('status'), {
      headers: { 'Accept': 'application/json' }
    });
    if (!resp.ok) {
      throw new Error('HTTP ' + resp.status);
    }
    errorEl.textContent = '';
    render(await resp.json());
  } catch (err) {
    errorEl.textContent = '加载状态失败: ' + err.message;
  }
}

async function downloadExport() {
  const btn = document.getElementById('export-btn');
  const origText = btn ? btn.textContent : '';
  if (btn) {
    btn.disabled = true;
    btn.textContent = '正在导出...';
  }
  try {
    const resp = await fetch(resolveUrl('export?limit=50'), {
      headers: { 'Accept': 'application/json' }
    });
    if (!resp.ok) {
      throw new Error('HTTP ' + resp.status);
    }
    const blob = await resp.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'cpa-auto-proxy-test-export.json';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (err) {
    alert('导出诊断数据失败: ' + err.message);
  } finally {
    if (btn) {
      btn.disabled = false;
      btn.textContent = origText;
    }
  }
}

load();
setInterval(load, REFRESH_MS);
</script>
</body>
</html>`
}

// NotConfiguredHTML is the placeholder page shown before configuration is loaded.
func NotConfiguredHTML() string {
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head><meta charset="utf-8"><title>cpa-auto-proxy-test</title></head>
<body style="font-family: system-ui, sans-serif; padding: 1.5rem;">
<h1>CPA-Auto-Proxy-Test</h1>
<p>插件尚未加载配置。</p>
</body>
</html>`
}

// ManagementResponse mirrors pluginapi.ManagementResponse for the RPC
// envelope returned from management.handle. The host decodes into
// pluginapi.ManagementResponse, which has no json tags, so the wire field
// names are encoding/json defaults (StatusCode/Headers/Body); Body travels
// as base64 per []byte marshalling and decodes symmetrically host-side.
type ManagementResponse struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers,omitempty"`
	Body       []byte              `json:"Body,omitempty"`
}

// JsonManagementResponse builds a ManagementResponse serving raw JSON.
func JsonManagementResponse(statusCode int, body []byte) ManagementResponse {
	return ManagementResponse{
		StatusCode: statusCode,
		Headers:    map[string][]string{"Content-Type": {"application/json; charset=utf-8"}},
		Body:       body,
	}
}

// HtmlManagementResponse builds a ManagementResponse serving the status HTML.
func HtmlManagementResponse(statusCode int, page string) ManagementResponse {
	return ManagementResponse{
		StatusCode: statusCode,
		Headers:    map[string][]string{"Content-Type": {"text/html; charset=utf-8"}},
		Body:       []byte(page),
	}
}
