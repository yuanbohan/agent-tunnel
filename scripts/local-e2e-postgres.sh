#!/bin/sh

set -eu

image="${LOCAL_E2E_PG_IMAGE:-postgres:16.11-alpine}"
container="${LOCAL_E2E_PG_CONTAINER:-agent-tunnel-e2e-postgres}"
volume="${LOCAL_E2E_PG_VOLUME:-agent-tunnel-e2e-pgdata}"
port="${LOCAL_E2E_PG_PORT:-55432}"
host="${LOCAL_E2E_PG_HOST:-127.0.0.1}"
user="${LOCAL_E2E_PG_USER:-agentunnel}"
password="${LOCAL_E2E_PG_PASSWORD:-agentunnel}"
database="${LOCAL_E2E_PG_DATABASE:-agent_tunnel_e2e}"
ready_timeout="${LOCAL_E2E_PG_READY_TIMEOUT:-30}"

dsn="postgres://${user}:${password}@${host}:${port}/${database}?sslmode=disable"

usage() {
	echo "usage: $0 {up|down|reset|status|logs|dsn}" >&2
}

redacted_dsn() {
	printf '%s\n' "$dsn" | sed 's#^\(postgres://[^:]*:\)[^@]*@#\1<redacted>@#'
}

require_docker() {
	if ! command -v docker >/dev/null 2>&1; then
		echo "docker is required" >&2
		exit 1
	fi
}

container_exists() {
	docker container inspect "$container" >/dev/null 2>&1
}

container_running() {
	[ "$(docker inspect -f '{{.State.Running}}' "$container" 2>/dev/null || echo false)" = "true" ]
}

volume_exists() {
	docker volume inspect "$volume" >/dev/null 2>&1
}

health_status() {
	docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container" 2>/dev/null || echo unknown
}

pull_image_if_needed() {
	if ! docker image inspect "$image" >/dev/null 2>&1; then
		echo "pulling $image"
		docker pull "$image" >/dev/null
	fi
}

wait_for_ready() {
	started_at="$(date +%s)"
	while :; do
		if ! container_running; then
			echo "container $container is not running" >&2
			docker logs --tail 50 "$container" >&2 || true
			exit 1
		fi

		health="$(health_status)"
		if [ "$health" = "unhealthy" ]; then
			echo "container $container reported unhealthy" >&2
			docker logs --tail 50 "$container" >&2 || true
			exit 1
		fi
		if [ "$health" = "healthy" ]; then
			if docker exec "$container" psql -U "$user" -d "$database" -c 'select 1' >/dev/null 2>&1; then
				return 0
			fi
		fi

		now="$(date +%s)"
		if [ $((now - started_at)) -ge "$ready_timeout" ]; then
			if [ "$health" = "healthy" ]; then
				echo "container $container is healthy but SQL probe failed" >&2
			else
				echo "timed out waiting for $container to become healthy" >&2
			fi
			docker logs --tail 50 "$container" >&2 || true
			exit 1
		fi
		sleep 1
	done
}

server_version() {
	docker exec "$container" psql -U "$user" -d "$database" -Atqc 'show server_version'
}

start_container() {
	pull_image_if_needed
	docker volume create "$volume" >/dev/null

	if container_exists; then
		if container_running; then
			echo "container $container is already running"
		else
			echo "starting existing container $container"
			docker start "$container" >/dev/null
		fi
	else
		echo "creating container $container from $image"
		docker run -d \
			--name "$container" \
			-p "${port}:5432" \
			-v "${volume}:/var/lib/postgresql/data" \
			-e "POSTGRES_USER=${user}" \
			-e "POSTGRES_PASSWORD=${password}" \
			-e "POSTGRES_DB=${database}" \
			--health-cmd 'pg_isready -U "$POSTGRES_USER" -d "$POSTGRES_DB"' \
			--health-interval 1s \
			--health-timeout 5s \
			--health-retries 30 \
			"$image" >/dev/null
	fi

	wait_for_ready

	echo "postgres ready"
	echo "  image: $image"
	echo "  server_version: $(server_version)"
	echo "  container: $container"
	echo "  port: $port"
	echo "  dsn: $(redacted_dsn)"
}

stop_container() {
	if ! container_exists; then
		echo "container $container does not exist"
		return
	fi

	if container_running; then
		docker stop "$container" >/dev/null
		echo "stopped $container"
	fi

	docker rm "$container" >/dev/null
	echo "removed $container"
}

remove_volume() {
	if ! volume_exists; then
		echo "volume $volume does not exist"
		return 0
	fi

	if docker volume rm -f "$volume" >/dev/null; then
		echo "removed volume $volume"
		return 0
	fi

	echo "failed to remove volume $volume" >&2
	return 1
}

show_status() {
	if ! container_exists; then
		echo "container: $container (missing)"
		echo "image: $image"
		echo "dsn: $(redacted_dsn)"
		return
	fi

	echo "container: $container"
	echo "image: $image"
	echo "running: $(docker inspect -f '{{.State.Running}}' "$container")"
	echo "health: $(health_status)"
	echo "port: $port"
	echo "dsn: $(redacted_dsn)"

	if container_running; then
		if docker exec "$container" psql -U "$user" -d "$database" -c 'select 1' >/dev/null 2>&1; then
			echo "sql_probe: ok"
			echo "server_version: $(server_version)"
		else
			echo "sql_probe: failed"
			exit 1
		fi
	fi
}

show_logs() {
	if ! container_exists; then
		echo "container $container does not exist" >&2
		exit 1
	fi
	docker logs --tail 100 "$container"
}

command="${1:-}"

require_docker

case "$command" in
	up)
		start_container
		;;
	down)
		stop_container
		;;
	reset)
		stop_container
		remove_volume
		;;
	status)
		show_status
		;;
	logs)
		show_logs
		;;
	dsn)
		printf '%s\n' "$dsn"
		;;
	*)
		usage
		exit 1
		;;
esac
