# 安全策略

## 支持版本

EffChat 当前仍是预发布软件。安全修复只提供给公开 `main` 分支的最新 commit 与最新发布的测试版。

## 报告安全问题

请勿通过公开 GitHub Issue 报告安全漏洞。

请使用 [GitHub Private Vulnerability Reporting](https://github.com/huoguojun123/EffChat/security/advisories/new) 私下报告，并尽量包含：

- 受影响的版本或 commit；
- 与问题有关的部署条件；
- 使用虚构数据编写的复现步骤或最小概念验证；
- 预期影响；
- 已知的缓解方式。

不要提交真实凭据、个人数据、数据库 dump 或生产文件。示例必须使用虚构数据，请求日志必须完成脱敏。

维护者会尽量在 7 天内确认收到报告，随后验证问题、协调修复与披露时间；除非报告者要求匿名，发布时会致谢报告者。

## 部署责任

EffChat 是自托管软件。实例运营者负责使用强数据库与 JWT secret、配置 TLS、维护备份与访问控制，并及时更新宿主机、容器、依赖和 EffChat 版本。

不要把运行中的 PostgreSQL 数据目录直接复制为备份。请使用文档中的 `scripts/backup-restore.sh backup` 工件流程，并通过严格目录权限、静态加密、异地副本、保留期限和安全删除保护备份目录。备份工件不会包含 `.env.docker` 与部署 secret。

恢复测试只能在独立空目录中执行 `scripts/backup-restore.sh restore`。该命令会建立隔离的 Compose project、网络、数据目录和 loopback 端口，不会覆盖活动部署或删除 volume。恢复后的数据库和文件仍属于敏感生产数据，验收后请使用 `scripts/backup-restore.sh stop-restore` 停止隔离环境。
