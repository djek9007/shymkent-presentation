.DEFAULT_GOAL := help

LOCAL_COMPOSE = docker compose -f platform/deploy/compose.local.yaml
DEV_COMPOSE = docker compose -f platform/deploy/compose.dev.yaml

.PHONY: help check install dev dev-down dev-logs dev-status dev-rebuild local-up local-down local-logs deploy deploy-status deploy-logs

help:
	@echo 'make dev           — локальная разработка с обновлением Vue без пересборки'
	@echo 'make dev-down      — остановить dev, сохранив данные'
	@echo 'make dev-rebuild   — пересобрать API после изменений Go'
	@echo 'make dev-status    — состояние dev-контейнеров'
	@echo 'make dev-logs      — последние dev-логи'
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

dev:
	$(DEV_COMPOSE) up -d --build --wait --wait-timeout 300
	@echo 'Портал: http://school.localhost:5173/'
	@echo 'Админка школы: http://demo-school-51.school.localhost:5173/admin/'

dev-down:
	$(DEV_COMPOSE) down

dev-rebuild:
	$(DEV_COMPOSE) up -d --build --wait --wait-timeout 300 api

dev-status:
	$(DEV_COMPOSE) ps

dev-logs:
	$(DEV_COMPOSE) logs --tail=50

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
