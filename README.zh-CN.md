# agyapi

**English** · [简体中文](README.zh-CN.md)

**把受地区限制的 Antigravity 账号，变成本地 OpenAI 兼容 API —— 不打补丁、不起子进程、不开 VPN。**

```bash
curl -s http://127.0.0.1:7862/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gemini-3.8-flash-high","messages":[{"role":"user","content":"你好"}]}'
```

---

## 为什么写它

我买了 **Google AI Pro**。账号本身完全正常，但在 Antigravity CLI 里它直接拒绝启动：

```text
error: Eligibility check failed: Your current account is not eligible for
Antigravity, because it is not currently available in your location.
```

地区判定为 **China**。于是就成了：付了钱的订阅、可用的凭据、一个不肯跑的客户端。

常见的绕法——打补丁、包一层 wrapper、把流量塞进 TUN 模式——都很脆：官方一更新就失效，而且都没有碰到真正的问题：**这个检查放错了位置**。

它确实放错了位置。同一个账号直接问后端，答案是另一个：

```json
POST /v1internal:loadCodeAssist
{
  "currentTier": { "id": "standard-tier", "name": "Antigravity" },
  "cloudaicompanionProject": "aicode-consumers",
  "ineligibleTiers": [ { "tierId": "free-tier", "reasonCode": "UNSUPPORTED_LOCATION" } ]
}
```

`currentTier` 是给了的；地区理由只挂在用不上的 `free-tier` 上。用这个账号的 OAuth token
请求 `v1internal:streamGenerateContent`，返回 `200` 和真实内容。

**也就是说，地区门禁在传输层是装饰性的。** 所以 `agyapi` 干脆不问 CLI：它直接按上游协议走
HTTPS，再翻译成 `/v1/chat/completions`。你如果已经维护着补丁二进制或 wrapper，这个项目可以替代它们。

---

## 工作原理

```text
OpenAI 客户端 ──POST /v1/chat/completions──► agyapi ──HTTPS──► daily-cloudcode-pa
(omp / Cursor / SDK)          :7862            │               .googleapis.com
                                              │               /v1internal:…
        ◄───── SSE 分块 ◄──────────────────────┘
```

- `tools[]` → Gemini `functionDeclarations`；`functionCall` → OpenAI `tool_calls`，不玩 XML 文本协议。
- 上游的 `thoughtSignature` 藏在 `tool_call_id` 里（`call_x__thought__<sig>`），多轮工具调用才能通过校验。
- `usageMetadata` 透传为 OpenAI `usage`，客户端做上下文统计时不再一片 0。
- 对话是结构化 `contents[]`，没有"单步提示词预算"可截断，也没有厂商自带的工具循环来劫持请求。

细节：[docs/architecture.md](docs/architecture.md) ·
[docs/upstream-notes.md](docs/upstream-notes.md) ·
[docs/troubleshooting.md](docs/troubleshooting.md)

---

## 快速开始

### 前置条件

- Go 1.24+（纯标准库，无第三方依赖）
- 一个在本机完成过一次登录的 Antigravity 账号

### 1. 构建

```bash
git clone https://github.com/yaping-pro/agyapi.git && cd agyapi
make build          # 或 go build -o agyapi ./cmd/agyapi
```

也可直接安装：`go install github.com/yaping-pro/agyapi/cmd/agyapi@latest`。

### 2. 指向你的凭据

token 文件就是交互式登录留下的那份。默认查找顺序：

```text
~/.local/share/agyapi/home/.gemini/antigravity-cli/antigravity-oauth-token
~/.gemini/antigravity-cli/antigravity-oauth-token            # 回退
```

用 `-token-path` 或 `AGY_TOKEN_PATH` 覆盖。文件是 JSON，含 `token.access_token` /
`token.refresh_token` / `token.expiry`；网关就地在原地刷新（会轮换 refresh token），以 `0600`
原子写回。

还没有 token？跑一次交互式登录即可。仓库自带一个把 `HOME` 隔离到网关数据目录的包装脚本：

```bash
./scripts/agy-login      # 出现 Keychain 弹窗时怎么处理见 docs/troubleshooting.md
```

### 3. 启动

```bash
./agyapi                          # 127.0.0.1:7862
./agyapi -port 8080 -verbose
```

```bash
curl -s http://127.0.0.1:7862/health
# {"status":"ok","version":"1.1.0","token_ready":true,...}
```

### 4. 接入客户端

任何 OpenAI 兼容客户端都能用：

```yaml
providers:
  antigravity-local:
    baseUrl: http://127.0.0.1:7862/v1
    api: openai-completions
    auth: none          # 仅回环地址
    discovery:
      type: openai-models-list
```

```python
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:7862/v1", api_key="local")
print(client.chat.completions.create(
    model="gemini-3.8-flash-high",
    messages=[{"role": "user", "content": "你好"}],
).choices[0].message.content)
```

---

## 配置

命令行参数与环境变量等价，参数优先。

| 参数 | 环境变量 | 默认值 | 含义 |
| :--- | :--- | :--- | :--- |
| `-host` | `HOST` | `127.0.0.1` | 绑定地址 |
| `-port` | `PORT` | `7862` | 监听端口 |
| `-token-path` | `AGY_TOKEN_PATH` | 见上 | OAuth token 文件 |
| `-project` | `AGY_PROJECT` | `aicode-consumers` | 信封里的 `cloudaicompanionProject` |
| `-upstream-url` | `AGY_UPSTREAM_URL` | `https://daily-cloudcode-pa.googleapis.com` | 上游基址 |
| `-default-model` | `DEFAULT_MODEL` | `gemini-3.8-flash-high` | 兜底模型 |
| `-max-concurrency` | `MAX_CONCURRENCY` | `4` | 上游并发上限 |
| `-verbose` | `VERBOSE` | `false` | 调试日志 |
| — | `AGY_OAUTH_TOKEN_URL` | `https://oauth2.googleapis.com/token` | 刷新端点 |
| — | `AGY_OAUTH_CLIENT_ID` / `_SECRET` | 厂商安装型客户端 | 轮换后可覆盖 |

---

## 模型

客户端选择器会被映射到上游 id 与思考档位：

| 选择器 | 上游模型 | 思考档 |
| :--- | :--- | :--- |
| `gemini-3.8-flash-low` | `gemini-3.8-flash-tiered` | `LOW` |
| `gemini-3.8-flash` / `-medium` | `gemini-3.8-flash-tiered` | `MEDIUM` |
| `gemini-3.8-flash-high` | `gemini-3.8-flash-tiered` | `HIGH` |
| `claude-sonnet-4-6` | `claude-sonnet-4-6` | — |
| `claude-opus-4-6-thinking` | `claude-opus-4-6-thinking` | — |
| `gpt-oss-120b-medium` | `gpt-oss-120b-medium` | — |
| `gemini-3-flash` | `gemini-3-flash` | — |

未知名字原样透传，账号新开通的模型无需改代码。`GET /v1/models` 列出本构建公布的清单。

---

## 安全模型

- 绑定 `127.0.0.1`。**没有鉴权层** —— 能连上端口的人就能消耗你的额度，别直接暴露到公网或局域网。
- token 文件 `0600`，原子写回；网关**不碰 macOS Keychain**，任何日志级别都不会打印 token。
- 单用户设计：轮换、并发与配额记账都不是多租户安全的。

详见 [SECURITY.md](SECURITY.md)。

---

## 已知限制

- 单账号，不做多账号池化。
- 无状态：不存会话历史，除 `sessionId` 亲和外无额外缓存。
- 非官方协议。`docs/upstream-notes.md` 记录的是实测结果，上游可以随时改。
- 暂不支持图像生成（`inlineData` 输入已支持，输出未接线）。

---

## 开发

```bash
make check        # gofmt + vet + test（与 CI 一致）
make test-race
make build
```

目录职责与约定见 [CONTRIBUTING.md](CONTRIBUTING.md)，版本历史见 [CHANGELOG.md](CHANGELOG.md)。

版本号唯一真源在 `internal/server/version.go`，由链接器注入：`make build VERSION=1.2.0` 即可产出正确自报版本的二进制。

## 许可证

[MIT](LICENSE)。

## 免责声明

本项目是独立的互操作工具，与 Google 无任何隶属、背书或支持关系。反向代理订阅可能违反服务条款，
风险由使用者承担。请仅用于你本人拥有、且你有权使用的账号。