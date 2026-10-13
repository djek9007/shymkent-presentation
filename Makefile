.DEFAULT_GOAL := help

LOCAL_COMPOSE = docker compose -f platform/deploy/compose.local.yaml

.PHONY: help check install local-up local-down local-logs deploy deploy-status deploy-logs

help:
	@echo 'make install       — установить зависимости фронтенда'
	@echo 'make check         — Go tests/vet и сборка фронтенда'
	@echo 'make local-up      — собрать и запустить локальный Docker'
	@echo 'make local-down    — остановить локальный Docker, сохранив данные'
	@echo 'make local-logs    — последние локальные логи'
	@echo 'make deploy        — выложить отправленную origin/main на Zangar'
	@echo 'make deploy-status — состояние контейнеров на Zangar'
	@echo 'make deploy-logs   — последние логи API и web на Zangar'

install:
	cd platform/web && npm ci

check:
	cd platform && go test ./... && go vet ./...
	cd platform/web && npm run build

local-up:
	$(LOCAL_COMPOSE) up -d --build --wait --wait-timeout 180

local-down:
	$(LOCAL_COMPOSE) down

local-logs:
	$(LOCAL_COMPOSE) logs --tail=50

deploy:
	@test -z "$$(git status --porcelain)" || { echo 'Сначала закоммитьте изменения.' >&2; exit 1; }
	@test "$$(git branch --show-current)" = main || { echo 'Деплой выполняется из ветки main.' >&2; exit 1; }
	git fetch origin main
	@test "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/main)" || { echo 'Локальная main должна совпадать с origin/main: выполните push или pull.' >&2; exit 1; }
	ssh zangar 'cd /home/ubuntu/projects/school-test && sh platform/scripts/deploy-test.sh'

deploy-status:
	ssh zangar 'cd /home/ubuntu/projects/school-test && docker compose --env-file /home/ubuntu/.config/school-test/database.env -f platform/deploy/compose.test.yaml ps'

deploy-logs:
	ssh zangar 'cd /home/ubuntu/projects/school-test && docker compose --env-file /home/ubuntu/.config/school-test/database.env -f platform/deploy/compose.test.yaml logs --tail=50 api web'
