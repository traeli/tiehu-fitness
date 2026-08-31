#!/bin/sh
set -eu

require_acl_name() {
  name="$1"
  value="$2"
  case "$value" in
    "" | *[!a-zA-Z0-9_-]*)
      echo "$name must contain only letters, digits, underscore, and hyphen" >&2
      exit 1
      ;;
  esac
}

require_acl_password() {
  name="$1"
  value="$2"
  case "$value" in
    "" | *[!a-zA-Z0-9._-]*)
      echo "$name must contain only letters, digits, dot, underscore, and hyphen" >&2
      exit 1
      ;;
  esac
  if [ "${#value}" -lt 16 ]; then
    echo "$name must contain at least 16 characters" >&2
    exit 1
  fi
}

require_acl_name REDIS_ADMIN_USERNAME "$REDIS_ADMIN_USERNAME"
require_acl_name CORE_REDIS_USERNAME "$CORE_REDIS_USERNAME"
require_acl_name VISION_REDIS_USERNAME "$VISION_REDIS_USERNAME"
require_acl_password REDIS_ADMIN_PASSWORD "$REDIS_ADMIN_PASSWORD"
require_acl_password CORE_REDIS_PASSWORD "$CORE_REDIS_PASSWORD"
require_acl_password VISION_REDIS_PASSWORD "$VISION_REDIS_PASSWORD"

umask 077
acl_file=/data/tiehu-users.acl
{
  echo "user default off"
  echo "user $REDIS_ADMIN_USERNAME on >$REDIS_ADMIN_PASSWORD ~* +@all"
  echo "user $CORE_REDIS_USERNAME on >$CORE_REDIS_PASSWORD ~core:* +@all -@dangerous"
  echo "user $VISION_REDIS_USERNAME on >$VISION_REDIS_PASSWORD ~vision:* +@all -@dangerous"
} > "$acl_file"

exec redis-server \
  --appendonly yes \
  --aclfile "$acl_file"
