PROJECT := go2serve

.PHONY: build up down restart logs status

build:
	docker compose -p $(PROJECT) build

up: build
	docker compose -p $(PROJECT) up -d

down:
	docker compose -p $(PROJECT) down

restart:
	docker compose -p $(PROJECT) restart

logs:
	docker compose -p $(PROJECT) logs -f

status:
	docker compose -p $(PROJECT) ps
