# Remnawave Limiter 简体中文支持

本 fork 在上游的俄语、英语基础上增加简体中文，保持原有业务逻辑及默认语言不变。

## 汉化范围

- `translations` 新增 `zh`，包含 171 个词条，覆盖 Telegram 告警、操作按钮、设置、统计、启动和恢复通知，以及原本已国际化的日志。
- 配置校验增加 `LANGUAGE=zh`。
- 未改动连接采集、违规判断、封禁、恢复或数据存储逻辑。
- 硬编码俄语日志和底层错误未提取翻译；部分错误仍可能显示俄语。
- 默认语言仍为 `ru`；`ru`、`en` 继续可用。语言修改需要重新创建容器。

## Docker 部署

GitHub Actions 为本仓库构建并发布 `linux/amd64`、`linux/arm64` 镜像：

```text
ghcr.io/avsba001/remnawave-limiter:latest
```

首次部署下载本 fork 的配置：

```bash
mkdir -p /opt/remnawave-limiter
cd /opt/remnawave-limiter
curl -fLO https://raw.githubusercontent.com/avsba001/remnawave-limiter/master/docker-compose.yml
curl -fLO https://raw.githubusercontent.com/avsba001/remnawave-limiter/master/.env.example
cp .env.example .env
```

填写 `.env` 中的面板 API、Telegram 及其他必要参数，并设置：

```env
LANGUAGE=zh
```

然后启动：

```bash
docker compose pull
docker compose up -d
docker compose logs -f limiter
```

已部署用户请先将 Compose 的 limiter 镜像改为上述 fork 镜像，再将 `.env` 中的语言设为 `zh`。不要覆盖已有 `.env` 或删除数据卷。

## 构建与测试

推送到 `master` 自动构建，也可在 Actions 的 `Build and Push Docker Image` 中手动运行。工作流先执行国际化与配置测试，再构建并推送镜像。

```bash
go test ./internal/i18n ./internal/config
docker build -t remnawave-limiter:zh .
```

其他功能与配置参阅 [英文文档](README_en.md) 或 [俄语文档](README.md)。项目沿用上游 GPL-3.0 许可证。
