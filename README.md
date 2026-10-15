# xray-endpoints-prober

Cloud-native сервис на Go для мониторинга доступности, задержки (RTT/TTFB) и скорости скачивания через прокси-эндпоинты Xray (VLESS, VMess, Trojan, Shadowsocks) с экспортом метрик в формате Prometheus.

## Возможности
- **Автоматическая загрузка Xray-core**: скачивание релизов с GitHub Releases для текущей архитектуры (`amd64`, `arm64`, etc.) и кеширование бинарника.
- **Опрос подписок**: поддержка Base64 списков (VLESS, VMess, Trojan, Shadowsocks) и Clash YAML конфигураций, сохранение в локальный кеш с fallback при сбоях сети, фильтрация по `PROTOCOLS_EXCLUDE`.
- **Генерация конфигурации Xray**: создание локальных SOCKS5/HTTP inbound-портов и outbound-маршрутов для каждого рабочего эндпоинта.
- **Оркестрация процесса Xray**: автозапуск, плавный перезапуск при изменении списка эндпоинтов и корректное завершение работы (SIGINT, SIGTERM).
- **Периодический пробинг**:
  - **Latency runner**: пул воркеров для замера RTT/TTFB к целевому URL через локальный SOCKS5 прокси.
  - **Speedtest runner**: последовательные замеры скорости скачивания через целевой URL.
- **Prometheus метрики**: экспорт метрик на `/metrics`.
- **Healthcheck эндпоинты**: `/healthz` (liveness) и `/readyz` (readiness).

## Переменные окружения

| Переменная | По умолчанию | Описание |
|---|---|---|
| `REGION` | `default` | Идентификатор региона / метка в метриках |
| `XRAY_VERSION` | `v24.11.30` | Версия Xray-core для загрузки |
| `XRAY_CACHE_DIR` | `/tmp/xray-cache` | Директория для кеширования бинарника и конфига Xray |
| `SUBSCRIPTION_URL` | `""` | URL подписки со списком прокси-серверов |
| `SUBSCRIPTION_POLL_INTERVAL` | `10m` | Интервал обновления подписки |
| `SUBSCRIPTION_CACHE_FILE` | `/tmp/xray-prober-subscription.cache` | Файл локального кеша подписки |
| `PROTOCOLS_EXCLUDE` | `""` | Исключаемые протоколы (через запятую, например `ss,trojan`) |
| `PROBE_PING_URL` | `https://cp.cloudflare.com/generate_204` | URL для замера RTT/TTFB |
| `PROBE_PING_INTERVAL` | `30s` | Интервал замера задержки |
| `PROBE_PING_TIMEOUT` | `5s` | Таймаут замера задержки |
| `PROBE_SPEEDTEST_URL` | `""` | URL файла для замера скорости скачивания |
| `PROBE_SPEEDTEST_INTERVAL` | `15m` | Интервал замера скорости |
| `PROBE_SPEEDTEST_TIMEOUT` | `30s` | Таймаут замера скорости |
| `PROBE_CONCURRENCY` | `5` | Количество параллельных воркеров для замера задержки |
| `LISTEN_ADDR` | `:8080` | Адрес HTTP-сервера для метрик и проб |

## Экспортируемые метрики

- `xray_prober_endpoint_up{endpoint_id, protocol, server, port, region}`
- `xray_prober_endpoint_latency_seconds{endpoint_id, protocol, server, port, region}`
- `xray_prober_endpoint_speed_bytes_per_second{endpoint_id, protocol, server, port, region}`
- `xray_prober_endpoint_speed_duration_seconds{endpoint_id, protocol, server, port, region}`
- `xray_prober_endpoint_probe_errors_total{endpoint_id, protocol, server, port, region, type}`
- `xray_prober_subscription_endpoints_count{region}`
- `xray_prober_subscription_last_poll_timestamp_seconds{region}`
- `xray_prober_subscription_poll_errors_total{region}`
- `xray_prober_core_version_info{region, version}`

## Запуск в Docker

```bash
docker build -t xray-endpoints-prober .
docker run -d \
  --name xray-endpoints-prober \
  -p 8080:8080 \
  -e SUBSCRIPTION_URL="https://example.com/sub" \
  -e PROBE_SPEEDTEST_URL="https://speed.cloudflare.com/__down?bytes=10000000" \
  xray-endpoints-prober
```
