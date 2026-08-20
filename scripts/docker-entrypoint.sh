#!/bin/sh
set -eu

fail() {
  echo "weaveflow container: $*" >&2
  exit 1
}

valid_port() {
  case "$1" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

valid_prefix() {
  case "$1" in
    '') return 0 ;;
    //*) return 1 ;;
    /*) ;;
    *) return 1 ;;
  esac
  case "$1" in
    *[!A-Za-z0-9._~/-]*) return 1 ;;
  esac
}

valid_backend_url() {
  case "$1" in
    http://*|https://*) ;;
    /*)
      case "$1" in
        //*) return 1 ;;
        *[!A-Za-z0-9._~/-]*) return 1 ;;
      esac
      ;;
    *) return 1 ;;
  esac
  case "$1" in
    *\"*|*\\*) return 1 ;;
  esac
  ! printf '%s' "$1" | LC_ALL=C grep -q '[^[:print:]]'
}

: "${WEAVEFLOW_WEB_PORT:=8080}"
: "${WEAVEFLOW_SERVER_PORT:=8081}"
: "${WEAVEFLOW_WEB_BACKEND_URL:=/api}"
: "${WEAVEFLOW_CORS_ORIGINS:=http://localhost:${WEAVEFLOW_WEB_PORT},http://127.0.0.1:${WEAVEFLOW_WEB_PORT}}"
: "${WEAVEFLOW_DATA_DIR:=/data/server}"
: "${WEAVEFLOW_SECRET_DIR:=/data/secrets}"
: "${WEAVEFLOW_LOG_LEVEL:=info}"
: "${WEAVEFLOW_SERVER_PREFIX:=}"

valid_port "${WEAVEFLOW_WEB_PORT}" || fail "WEAVEFLOW_WEB_PORT must be an integer between 1 and 65535"
valid_port "${WEAVEFLOW_SERVER_PORT}" || fail "WEAVEFLOW_SERVER_PORT must be an integer between 1 and 65535"
[ "${WEAVEFLOW_WEB_PORT}" -ne "${WEAVEFLOW_SERVER_PORT}" ] || fail "WEAVEFLOW_WEB_PORT and WEAVEFLOW_SERVER_PORT must be different"
valid_backend_url "${WEAVEFLOW_WEB_BACKEND_URL}" || fail "WEAVEFLOW_WEB_BACKEND_URL must be an http(s) URL or same-origin path without quotes or control characters"

if [ -n "${WEAVEFLOW_SERVER_PREFIX}" ] && [ "${WEAVEFLOW_SERVER_PREFIX#/}" = "${WEAVEFLOW_SERVER_PREFIX}" ]; then
  WEAVEFLOW_SERVER_PREFIX="/${WEAVEFLOW_SERVER_PREFIX}"
fi
while [ "${WEAVEFLOW_SERVER_PREFIX%/}" != "${WEAVEFLOW_SERVER_PREFIX}" ]; do
  WEAVEFLOW_SERVER_PREFIX="${WEAVEFLOW_SERVER_PREFIX%/}"
done
[ "${WEAVEFLOW_SERVER_PREFIX}" = "/" ] && WEAVEFLOW_SERVER_PREFIX=""
valid_prefix "${WEAVEFLOW_SERVER_PREFIX}" || fail "WEAVEFLOW_SERVER_PREFIX contains unsupported characters"

export WEAVEFLOW_WEB_PORT
export WEAVEFLOW_SERVER_PORT
export WEAVEFLOW_WEB_BACKEND_URL
export WEAVEFLOW_SERVER_PREFIX

mkdir -p "${WEAVEFLOW_DATA_DIR}" "${WEAVEFLOW_SECRET_DIR}"
mkdir -p /tmp/weaveflow /tmp/nginx-client-body /tmp/nginx-proxy-temp /tmp/nginx-fastcgi-temp /tmp/nginx-uwsgi-temp /tmp/nginx-scgi-temp

envsubst '${WEAVEFLOW_WEB_PORT} ${WEAVEFLOW_SERVER_PORT} ${WEAVEFLOW_SERVER_PREFIX}' \
  < /etc/weaveflow/nginx.conf.template \
  > /tmp/nginx.conf
envsubst '${WEAVEFLOW_WEB_BACKEND_URL}' \
  < /etc/weaveflow/web-config.js.template \
  > /tmp/weaveflow/config.js.tmp
mv /tmp/weaveflow/config.js.tmp /tmp/weaveflow/config.js

set -- /app/weaveflow-server \
  -addr "127.0.0.1:${WEAVEFLOW_SERVER_PORT}" \
  -data "${WEAVEFLOW_DATA_DIR}" \
  -secret-dir "${WEAVEFLOW_SECRET_DIR}" \
  -cors-origins "${WEAVEFLOW_CORS_ORIGINS}" \
  -log-level "${WEAVEFLOW_LOG_LEVEL}"

if [ -n "${WEAVEFLOW_GRAPH:-}" ]; then
  set -- "$@" -graph "${WEAVEFLOW_GRAPH}"
fi

if [ -n "${WEAVEFLOW_SERVER_PREFIX}" ]; then
  set -- "$@" -prefix "${WEAVEFLOW_SERVER_PREFIX}"
fi

"$@" &
server_pid=$!

cleanup() {
  if kill -0 "${server_pid}" 2>/dev/null; then
    kill -TERM "${server_pid}" 2>/dev/null || true
  fi
  if kill -0 "${nginx_pid:-}" 2>/dev/null; then
    kill -TERM "${nginx_pid}" 2>/dev/null || true
  fi
  wait "${server_pid}" 2>/dev/null || true
  if [ -n "${nginx_pid:-}" ]; then
    wait "${nginx_pid}" 2>/dev/null || true
  fi
}

handle_signal() {
  cleanup
  exit 143
}

trap cleanup EXIT
trap handle_signal INT TERM
nginx -c /tmp/nginx.conf -g 'daemon off;' &
nginx_pid=$!

while :; do
  if ! kill -0 "${server_pid}" 2>/dev/null; then
    server_status=0
    wait "${server_pid}" || server_status=$?
    exit "${server_status}"
  fi
  if ! kill -0 "${nginx_pid}" 2>/dev/null; then
    nginx_status=0
    wait "${nginx_pid}" || nginx_status=$?
    exit "${nginx_status}"
  fi
  sleep 1
done
