.PHONY: migrate-up migrate-down postgres generate-sqlc go-get

# --- Migrations ----

migrate-up:
	docker compose exec api sh -c 'goose -dir migrations postgres "$$DATABASE_URL" up'

migrate-down:
	docker compose exec api sh -c 'goose -dir migrations postgres "$$DATABASE_URL" down'




# ----- postgres (db) ----------
postgres:
	docker compose exec postgres psql -U ownS3 -d ownS3 -c "$(wordlist 2, $(words $(MAKECMDGOALS)), $(MAKECMDGOALS))"



# ----- generate ---------
generate-sqlc:
	docker compose exec api go tool sqlc generate


# ----- golang (go)--------
go-get:
	docker compose exec api go get "$(pkg)"
