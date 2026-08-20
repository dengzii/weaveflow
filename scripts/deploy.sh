#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
project_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
docker_command=${DOCKER_BIN:-docker}
version=$(sed -n '1p' "$project_root/VERSION")
image=${WEAVEFLOW_IMAGE:-weaveflow:${version}}
container=${WEAVEFLOW_CONTAINER:-weaveflow}
publish_host=${WEAVEFLOW_PUBLISH_HOST:-127.0.0.1}
publish_port=${WEAVEFLOW_PUBLISH_PORT:-8080}
web_port=${WEAVEFLOW_WEB_PORT:-8080}
workspace=${WEAVEFLOW_WORKSPACE:-$project_root}
data_volume=${WEAVEFLOW_DATA_VOLUME:-weaveflow-data}
env_file=${WEAVEFLOW_ENV_FILE:-$project_root/.env}

usage() {
  cat <<EOF
Usage: $0 <command>

Commands:
  build    Build the container image
  deploy   Build the image and create the container
  run      Create the container from an existing image
  status   Show the container status
  logs     Follow container logs
  stop     Stop the container
  restart  Restart the container
  remove   Remove the container, keeping its data volume
EOF
}

fail() {
  echo "deploy: $*" >&2
  exit 1
}

valid_port() {
  case "$1" in
    ''|*[!0-9]*) return 1 ;;
  esac
  [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

require_docker() {
  command -v "$docker_command" >/dev/null 2>&1 || fail "Docker CLI not found: $docker_command"
  "$docker_command" info >/dev/null 2>&1 || fail "Docker daemon is unavailable; check the Docker service and group permissions"
}

build_image() {
  version_arg=${WEAVEFLOW_VERSION:-$version}
  vcs_ref=${WEAVEFLOW_VCS_REF:-}
  if [ -z "$vcs_ref" ] && command -v git >/dev/null 2>&1; then
    vcs_ref=$(git -C "$project_root" describe --always --dirty 2>/dev/null || true)
  fi
  : "${vcs_ref:=unknown}"
  if [ "${WEAVEFLOW_PULL_BASE_IMAGES:-0}" = "1" ]; then
    "$docker_command" build --pull \
      --build-arg "VERSION=${version_arg}" \
      --build-arg "VCS_REF=${vcs_ref}" \
      -f "$script_dir/Dockerfile" -t "$image" "$project_root"
  else
    "$docker_command" build \
      --build-arg "VERSION=${version_arg}" \
      --build-arg "VCS_REF=${vcs_ref}" \
      -f "$script_dir/Dockerfile" -t "$image" "$project_root"
  fi
}

run_container() {
  valid_port "$publish_port" || fail "WEAVEFLOW_PUBLISH_PORT must be an integer between 1 and 65535"
  valid_port "$web_port" || fail "WEAVEFLOW_WEB_PORT must be an integer between 1 and 65535"
  [ -d "$workspace" ] || fail "workspace directory does not exist: $workspace"
  "$docker_command" image inspect "$image" >/dev/null 2>&1 || fail "image does not exist: $image; run '$0 build' first"
  if "$docker_command" container inspect "$container" >/dev/null 2>&1; then
    fail "container already exists: $container; use '$0 stop' and '$0 remove' before deploying again"
  fi

  set -- "$docker_command" run --detach --init \
    --name "$container" \
    --restart unless-stopped \
    --read-only \
    --security-opt no-new-privileges:true \
    --cap-drop ALL \
    --tmpfs /tmp:rw,noexec,nosuid,size=64m \
    --publish "${publish_host}:${publish_port}:${web_port}" \
    --volume "${data_volume}:/data" \
    --volume "${workspace}:/workspace" \
    --env "WEAVEFLOW_WEB_PORT=${web_port}"
  if [ -f "$env_file" ]; then
    set -- "$@" --env-file "$env_file"
  fi
  set -- "$@" "$image"
  "$@"
}

[ "$#" -eq 1 ] || {
  usage
  exit 2
}

case "$1" in
  help|-h|--help)
    usage
    ;;
  build)
    require_docker
    build_image
    ;;
  deploy)
    require_docker
    build_image
    run_container
    ;;
  run)
    require_docker
    run_container
    ;;
  status)
    require_docker
    "$docker_command" ps -a --filter "name=^/${container}$"
    ;;
  logs)
    require_docker
    "$docker_command" logs --follow "$container"
    ;;
  stop)
    require_docker
    "$docker_command" stop "$container"
    ;;
  restart)
    require_docker
    "$docker_command" restart "$container"
    ;;
  remove)
    require_docker
    "$docker_command" rm -f "$container"
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
