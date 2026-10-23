# Soundcheck testbed: 20 broken-site scenarios on WordPress and plain PHP.
# Needs Docker with Compose v2. Ports: WordPress 18800, plain PHP 18810.

COMPOSE := docker compose -f docker-compose.yml
WP_URL  := http://localhost:$(or $(TESTBED_WP_PORT),18800)
PHP_URL := http://localhost:$(or $(TESTBED_PHP_PORT),18810)

.PHONY: up down reset good broken status logs wp

up: ## start and seed the sites (a fresh install every time)
	@mkdir -p state
	$(COMPOSE) up -d --wait db wp php
	$(COMPOSE) run --rm cli
	@for i in $$(seq 1 30); do curl -fsS -o /dev/null $(WP_URL)/ && curl -fsS -o /dev/null $(PHP_URL)/ && break; sleep 1; done
	@echo "WordPress: $(WP_URL)/  (admin/admin, Soundcheck user soundcheck/soundcheck-testbed)"
	@echo "Plain PHP: $(PHP_URL)/"
	@./toggle.sh status

down: ## stop and delete everything (database, WordPress files)
	$(COMPOSE) --profile tools down -v --remove-orphans

reset: down up ## start over from a freshly seeded site

good: ## make every scenario good, or one: make good ID=s05
	./toggle.sh $(or $(ID),all) good

broken: ## break one scenario (make broken ID=s05), or every one when ID is unset
	./toggle.sh $(or $(ID),all) broken

status: ## list broken scenarios
	@./toggle.sh status

logs: ## follow the web server logs
	$(COMPOSE) logs -f wp php

wp: ## run WP-CLI, e.g. make wp ARGS="post list --post_type=page"
	$(COMPOSE) run --rm --entrypoint wp cli $(ARGS)
