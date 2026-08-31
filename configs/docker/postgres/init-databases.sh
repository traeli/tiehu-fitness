#!/bin/sh
set -eu

require_identifier() {
  name="$1"
  value="$2"
  case "$value" in
    "" | *[!a-zA-Z0-9_]*)
      echo "$name must contain only letters, digits, and underscores" >&2
      exit 1
      ;;
  esac
}

require_password() {
  name="$1"
  value="$2"
  case "$value" in
    "" | *[!a-zA-Z0-9._-]*)
      echo "$name must contain only letters, digits, dot, underscore, and hyphen" >&2
      exit 1
      ;;
  esac
  if [ "${#value}" -lt 5 ]; then
    echo "$name must contain at least 5 characters" >&2
    exit 1
  fi
}

require_identifier CORE_DATABASE_USER "$CORE_DATABASE_USER"
require_identifier CORE_DATABASE_NAME "$CORE_DATABASE_NAME"
require_identifier VISION_DATABASE_USER "$VISION_DATABASE_USER"
require_identifier VISION_DATABASE_NAME "$VISION_DATABASE_NAME"
require_password CORE_DATABASE_PASSWORD "$CORE_DATABASE_PASSWORD"
require_password VISION_DATABASE_PASSWORD "$VISION_DATABASE_PASSWORD"

psql --set=ON_ERROR_STOP=1 \
  --username "$POSTGRES_USER" \
  --dbname "$POSTGRES_DB" \
  --set=core_user="$CORE_DATABASE_USER" \
  --set=core_password="$CORE_DATABASE_PASSWORD" \
  --set=core_database="$CORE_DATABASE_NAME" \
  --set=vision_user="$VISION_DATABASE_USER" \
  --set=vision_password="$VISION_DATABASE_PASSWORD" \
  --set=vision_database="$VISION_DATABASE_NAME" <<'SQL'
CREATE ROLE :"core_user" LOGIN PASSWORD :'core_password';
CREATE DATABASE :"core_database" OWNER :"core_user";
REVOKE ALL ON DATABASE :"core_database" FROM PUBLIC;

CREATE ROLE :"vision_user" LOGIN PASSWORD :'vision_password';
CREATE DATABASE :"vision_database" OWNER :"vision_user";
REVOKE ALL ON DATABASE :"vision_database" FROM PUBLIC;
SQL

apply_migrations() {
  service="$1"
  database_user="$2"
  database_password="$3"
  database_name="$4"

  for migration in "/tiehu-migrations/$service"/*.up.sql; do
    echo "applying $migration to $database_name"
    PGPASSWORD="$database_password" psql \
      --username "$database_user" \
      --dbname "$database_name" \
      --set=ON_ERROR_STOP=1 \
      --file "$migration"
  done
}

apply_migrations core "$CORE_DATABASE_USER" "$CORE_DATABASE_PASSWORD" "$CORE_DATABASE_NAME"
apply_migrations vision "$VISION_DATABASE_USER" "$VISION_DATABASE_PASSWORD" "$VISION_DATABASE_NAME"
