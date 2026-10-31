# 邮箱管理：设计

目标：任何用户都能在自己的电脑上绑定多个邮箱，聚合管理，并可筛选其中几个，由本地模型整理邮件。没有服务器，邮件不经过我们。

## 形态

- 可绑定多个邮箱；聚合收件箱把它们放在一起，左侧勾选决定显示和整理哪些。

- 起始页“智能”分类里的一张卡片，开在自己的标签页；固定标签页后随应用打开（`tabSpec`、`restoreTab` 各加一行）。
- 设置 → AI 模型里只放偏好：同步频率、用哪个模型。
- 打开邮件只显示纯文字，不加载图片和链接，发件人无从得知邮件被读过；服务器上的已读状态不变。
- 模型调用走 `ai.Runtime`（`Ensure` / `Endpoint`），不另起模型进程。

## 连接邮箱

程序在本地运行，也绕不开 Google 的规定：审核针对的是编进程序的 OAuth 客户端，所有用户共用它。`gmail.readonly`、`gmail.modify` 是受限权限，未审核的客户端总共只能有 100 个用户，并显示“未验证应用”警告；处于测试状态时令牌 7 天过期。要让任何人都能用，就要通过审核和第三方安全评估。

所以默认不用这条路，而用 **IMAP + 应用专用密码**：

| 方式 | 谁能用 | 要 Google 审核吗 | 缺点 |
|---|---|---|---|
| IMAP + 应用专用密码（默认） | 开了两步验证的个人 Gmail；iCloud、Yahoo、AOL、Fastmail、Zoho 等提供应用专用密码的邮箱 | 不要 | 用户要自己生成一个密码；Google 称它“不推荐”；改账号密码后失效 |

- 首次连接时由向导带用户生成密码，并打开对应的 Google 页面，用户粘贴进来。
- 密码和令牌用 `vault` 加密存放，不进任何 JSON 配置。
- Google 的 Workspace 账户已不支持这种密码登录，暂不支持。
- 不支持 Outlook、Hotmail、Live：微软只接受 OAuth，需要另外注册应用，已决定不做；填这些地址时界面直接说明。
- 预设的邮箱以美国常用的为先：Gmail、iCloud、Yahoo、AOL/Verizon、Proton（经本机 Bridge，STARTTLS）、Fastmail、Zoho、Xfinity、AT&T、GMX、mail.com，最后是 QQ、163 和任意服务器；输入地址时按域名自动选中。
- **风险要写在界面上**：应用专用密码能完全访问邮箱，技术上比 OAuth 分级权限大。“不发送、不删除”靠程序自己遵守：程序里没有 SMTP 代码，也没有 `EXPUNGE` 和删除操作，并用测试守住这一点。

## 本地存储与同步

- 保存在资源文件夹 `Intelligence/mail/`，每个账号一份。用纯 Go 的 SQLite（不引入 cgo），全文搜索现成；这是仓库的一个新依赖。
- 首次同步最近 90 天（可调），之后按 IMAP 的 `UIDVALIDITY` 和 `UID` 增量同步；`CONDSTORE` 可用时同步标记变化。
- Gmail 用 `X-GM-MSGID`、`X-GM-THRID`、`X-GM-LABELS` 取得稳定 id、会话和标签。
- 只存纯文本正文和元数据；附件按需下载。删除账号时一键清空本地数据。

## “整理”能做什么

| 动作 | 执行方式 |
|---|---|
| 摘要、优先级、分类 | 自动，只写在本地 |
| 打标签、归档 | 模型提建议，你确认后才执行；可对某条规则设为“自动”，但默认关闭 |
| 起草回复 | 用 IMAP `APPEND` 存进草稿箱，绝不发送 |
| 发送、删除 | 不做 |

- 每个写操作进本地日志：时间、邮件、动作、来自哪条建议，并有“撤销”（标签和归档都可逆）。
- 邮件内容是不可信输入：模型只输出固定 JSON 结构（动作 + 邮件 id），邮件里的文字不能触发动作，也不能扩大范围。
- 第一版只用本机模型，邮件不发给在线服务。

## 分步

1. 连接向导、只读同步、列表和阅读界面。
2. 本地摘要、优先级、分类。
3. 建议队列、确认后写回、操作日志和撤销。
4. 草稿。

## 待实现时验证

- Gmail 官方文档只写了用 `+X-GM-LABELS` 加标签，没写移除和归档；要用真实账号验证归档（去掉 `\Inbox` 标签或 `MOVE`）。
- 需要一个你提供的测试账号的真实连接来做最终验证；程序测试用本地假 IMAP 服务器，不碰真实邮箱。

## 来源

- [应用专用密码](https://support.google.com/mail/answer/185833?hl=en)
- [IMAP 仍受支持](https://support.google.com/mail/answer/16604719?hl=en)
- [Gmail IMAP 扩展](https://developers.google.com/gmail/imap/imap-extensions)
- [受限权限](https://developers.google.com/workspace/gmail/api/auth/scopes)
- [OAuth 验证常见问题](https://support.google.com/cloud/answer/9110914?hl=en-GB)
- [测试状态与用户上限](https://support.google.com/cloud/answer/15549945?hl=en)
