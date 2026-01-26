#!/usr/bin/env bash

compute_entrypoint_hash() {
    local entrypoint_path="$1"
    local user_script="$2"
    local current_hash=""

    if [ -f "$entrypoint_path" ]; then
        current_hash=$(sha256sum "$entrypoint_path" | awk '{print $1}')
    else
        return 0
    fi

    if [ -f "$user_script" ]; then
        local install_hash=""
        install_hash=$(sha256sum "$user_script" | awk '{print $1}')
        current_hash="${current_hash}-${install_hash}"
    fi

    echo "$current_hash"
}

write_entrypoint_hash() {
    local hash_file="$1"
    local entrypoint_path="$2"
    local user_script="$3"
    local current_hash=""

    current_hash=$(compute_entrypoint_hash "$entrypoint_path" "$user_script")
    if [ -z "$current_hash" ]; then
        return 0
    fi

    mkdir -p "$(dirname "$hash_file")"
    echo "$current_hash" > "$hash_file"
}
