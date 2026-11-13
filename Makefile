.PHONY: migrate-up migrate-down migrate-create postgres generate-sqlc clean-storage go-get compose-up compose-logs

# --- Migrations ----

migrate-up:
	docker compose exec api sh -c 'goose -dir migrations postgres "$$DATABASE_URL" up'

migrate-down:
	docker compose exec api sh -c 'goose -dir migrations postgres "$$DATABASE_URL" down'

migrate-create:
	docker compose exec api sh -c 'goose -dir migrations create -s "$(wordlist 2, $(words $(MAKECMDGOALS)), $(MAKECMDGOALS))" sql'


# ----- postgres (db) ----------
postgres:
	docker compose exec postgres psql -U ownS3 -d ownS3 -c "$(wordlist 2, $(words $(MAKECMDGOALS)), $(MAKECMDGOALS))"



# ----- generate ---------
generate-sqlc:
	docker compose exec api go tool sqlc generate


# ----- golang (go)--------
go-get:
	docker compose exec api go get "$(pkg)"


# ------- docker compose ---------
compose-up:
	docker compose up

compose-logs:
	docker compose logs -f "$(s)



# -------- clean -----------
clean-storage:
	sudo rm -rf ./internal/storage && mkdir -p ./internal/storage
