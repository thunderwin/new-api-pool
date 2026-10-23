#!/usr/bin/env bash
# Set the Bright Data API key used by web-unlock-v1.
#
# Usage:
#   ./set-capability-secret.sh
#   ./set-capability-secret.sh --dry-run
#
# The key is read without echo, sent over SSH stdin, and never appears in local
# or remote command-line arguments. The server keeps the value only in
# /opt/new-api-pool/.env (mode 600) and the recreated new-api container.
set -euo pipefail

ssh_target="${SSH_TARGET:-root@2.31.8.21}"
remote_dir="${REMOTE_DIR:-/opt/new-api-pool}"
ssh_args=()
if [[ -n "${SSH_IDENTITY_FILE:-}" ]]; then
	ssh_args=(-i "$SSH_IDENTITY_FILE")
fi
dry_run=0
if [[ "${1:-}" == "--dry-run" ]]; then
	dry_run=1
elif [[ -n "${1:-}" ]]; then
	echo "用法：$0 [--dry-run]" >&2
	exit 2
fi

remote_script=$(cat <<'REMOTE_SCRIPT'
set -euo pipefail
cd "$REMOTE_DIR"

if [[ ! -f .env ]]; then
	echo "未设置"
	exit 1
fi

capability_value=""
IFS= read -r capability_value || true
if [[ -z "$capability_value" ]]; then
	echo "未设置"
	exit 1
fi

timestamp=$(date +%Y%m%d%H%M%S)
backup=".env.bak-capability-$timestamp"
cp -p .env "$backup"
chmod 600 "$backup"

temp_env=$(mktemp .env.capability.XXXXXX)
found=0
{
	while IFS= read -r line || [[ -n "$line" ]]; do
		if [[ "$line" == CAPABILITY_BRIGHTDATA_API_KEY=* ]]; then
			if ((found == 0)); then
				printf '%s\n' "CAPABILITY_BRIGHTDATA_API_KEY=$capability_value"
				found=1
			fi
		else
			printf '%s\n' "$line"
		fi
	done < .env
	if ((found == 0)); then
		printf '%s\n' "CAPABILITY_BRIGHTDATA_API_KEY=$capability_value"
	fi
} > "$temp_env"
chmod 600 "$temp_env"
mv -f "$temp_env" .env
unset capability_value

docker compose up -d new-api >/dev/null
if docker compose exec -T new-api sh -c 'test -n "$CAPABILITY_BRIGHTDATA_API_KEY"' >/dev/null 2>&1; then
	echo "已设置"
else
	echo "未设置"
	exit 1
fi
REMOTE_SCRIPT
)
remote_script_b64=$(printf '%s' "$remote_script" | base64 | tr -d '\n')
remote_command="bash -c \"\$(printf '%s' '$remote_script_b64' | base64 -d)\""

if ((dry_run == 1)); then
	temp_root=$(mktemp -d)
	cleanup() { rm -rf "$temp_root"; }
	trap cleanup EXIT
	mkdir -p "$temp_root/bin"
	printf '%s\n' \
		'CAPABILITY_BRIGHTDATA_ZONE=wayfair_unlocker' \
		'CAPABILITY_BRIGHTDATA_API_KEY=old-dry-run-value' > "$temp_root/.env"
	chmod 600 "$temp_root/.env"
	cat > "$temp_root/bin/docker" <<'FAKE_DOCKER'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "compose" && "${2:-}" == "exec" ]]; then
	grep -q '^CAPABILITY_BRIGHTDATA_API_KEY=.' .env
fi
FAKE_DOCKER
	chmod 700 "$temp_root/bin/docker"
	printf '%s\n' 'dry-run-placeholder' | \
		env REMOTE_DIR="$temp_root" PATH="$temp_root/bin:$PATH" bash -c "$remote_script"
	if ! grep -q '^CAPABILITY_BRIGHTDATA_API_KEY=dry-run-placeholder$' "$temp_root/.env"; then
		echo "未设置"
		exit 1
	fi
	exit 0
fi

set +x
capability_key=""
trap 'unset capability_key' EXIT
read -r -s -p "Bright Data API key: " capability_key
printf '\n'
if [[ -z "$capability_key" ]]; then
	echo "未设置"
	exit 1
fi

if ! printf '%s\n' "$capability_key" | ssh "${ssh_args[@]}" "$ssh_target" "REMOTE_DIR='$remote_dir' $remote_command"; then
	echo "未设置"
	exit 1
fi
