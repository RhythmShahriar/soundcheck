# Soundcheck testbed: broken-site scenarios on WordPress and plain PHP.
# Needs Docker with Compose v2. Ports: WordPress 18800, plain PHP 18810
# (http), 18811 (https) and 18812 (the same site as another origin, for
# the cross-origin frame of s84).

COMPOSE := docker compose -f docker-compose.yml
WP_URL   := http://localhost:$(or $(TESTBED_WP_PORT),18800)
PHP_URL  := http://localhost:$(or $(TESTBED_PHP_PORT),18810)
PHPS_URL := https://localhost:$(or $(TESTBED_PHP_HTTPS_PORT),18811)

.PHONY: up down reset good broken status logs wp wp-update-reset wp-updates bench-eaa

up: ## start and seed the sites (a fresh install every time)
	@mkdir -p state
	@# The counters the sites write (the web servers run as www-data).
	@for f in orders sidefx; do touch state/$$f.txt && chmod 666 state/$$f.txt; done
	$(COMPOSE) up -d --wait db wp php
	$(COMPOSE) run --rm cli
	@for i in $$(seq 1 30); do curl -fsS -o /dev/null $(WP_URL)/ && curl -fsS -o /dev/null $(PHP_URL)/ && curl -kfsS -o /dev/null $(PHPS_URL)/ && break; sleep 1; done
	@echo "WordPress: $(WP_URL)/  (admin/admin, Soundcheck user soundcheck/soundcheck-testbed)"
	@echo "Plain PHP: $(PHP_URL)/ and $(PHPS_URL)/ (self-signed, use -k)"
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

wp-update-reset: ## put the ten testbed-update plugins back at 1.0 (s47, s48)
	$(COMPOSE) run --rm --no-deps -T --entrypoint sh cli /testbed-updates/reset.sh

wp-updates: ## list the plugin updates WordPress offers
	$(COMPOSE) run --rm --no-deps -T --entrypoint wp cli plugin list --update=available --fields=name,version,update_version

bench-eaa: ## run EAA mode's benchmark (s49 to s91, the --eaa control pass and the cost pass); needs make build first
	cd .. && go run -tags testbed ./testbed/bench -bin bin/rhythm-soundcheck -only eaa $(ARGS)
