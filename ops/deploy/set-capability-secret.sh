#!/usr/bin/env bash
# Set a Capability provider credential in the server-side .env.
#
# Usage:
#   ./set-capability-secret.sh
#   ./set-capability-secret.sh CAPABILITY_AISA_API_KEY
#   ./set-capability-secret.sh CAPABILITY_AISA_API_KEY --dry-run
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
secret_name="CAPABILITY_BRIGHTDATA_API_KEY"
for arg in "$@"; do
	case "$arg" in
	--dry-run)
		dry_run=1
		;;
	*)
		if [[ ! "$arg" =~ ^CAPABILITY_[A-Z0-9_]+$ ]]; then
			echo "变量名必须匹配 ^CAPABILITY_[A-Z0-9_]+$" >&2
			exit 2
		fi
		secret_name="$arg"
		;;
	esac
done

remote_script=$(cat <<'REMOTE_SCRIPT'
set -euo pipefail
cd "$REMOTE_DIR"

if [[ ! "${CAPABILITY_SECRET_NAME:-}" =~ ^CAPABILITY_[A-Z0-9_]+$ ]]; then
	echo "未设置"
	exit 1
fi
secret_name="$CAPABILITY_SECRET_NAME"

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
		if [[ "$line" == "$secret_name="* ]]; then
			if ((found == 0)); then
				printf '%s=%s\n' "$secret_name" "$capability_value"
				found=1
			fi
		else
			printf '%s\n' "$line"
		fi
	done < .env
	if ((found == 0)); then
		printf '%s=%s\n' "$secret_name" "$capability_value"
	fi
} > "$temp_env"
chmod 600 "$temp_env"
mv -f "$temp_env" .env
unset capability_value

docker compose up -d new-api >/dev/null
if docker compose exec -T new-api sh -c "test -n \"\${$secret_name}\"" >/dev/null 2>&1; then
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
		'CAPABILITY_BRIGHTDATA_API_KEY=old-dry-run-value' \
		'CAPABILITY_AISA_API_KEY=old-aisa-dry-run-value' > "$temp_root/.env"
	chmod 600 "$temp_root/.env"
	cat > "$temp_root/bin/docker" <<'FAKE_DOCKER'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "compose" && "${2:-}" == "exec" ]]; then
	last_arg="${@: -1}"
	name=$(printf '%s\n' "$last_arg" | sed -n 's/.*${\(CAPABILITY_[A-Z0-9_]*\)}.*/\1/p')
	[[ -n "$name" ]] && grep -q "^${name}=." .env
fi
FAKE_DOCKER
	chmod 700 "$temp_root/bin/docker"
	printf '%s\n' 'dry-run-placeholder' | \
		env REMOTE_DIR="$temp_root" CAPABILITY_SECRET_NAME="$secret_name" PATH="$temp_root/bin:$PATH" \
		bash -c "$remote_script"
	if ! grep -q "^${secret_name}=dry-run-placeholder$" "$temp_root/.env"; then
		echo "未设置"
		exit 1
	fi
	exit 0
fi

set +x
capability_key=""
trap 'unset capability_key' EXIT
read -r -s -p "$secret_name: " capability_key
printf '\n'
if [[ -z "$capability_key" ]]; then
	echo "未设置"
	exit 1
fi

if ! printf '%s\n' "$capability_key" | \
	ssh "${ssh_args[@]}" "$ssh_target" \
	"REMOTE_DIR='$remote_dir' CAPABILITY_SECRET_NAME='$secret_name' $remote_command"; then
	echo "未设置"
	exit 1
fi
