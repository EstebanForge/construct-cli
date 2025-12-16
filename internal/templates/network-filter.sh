#!/usr/bin/env bash
# network-filter.sh - Configure network filtering using ufw
# Called from entrypoint.sh when NETWORK_MODE=strict

set -e

# Only run in strict mode
if [ "$NETWORK_MODE" != "strict" ]; then
    exit 0
fi

echo "🔒 Configuring network filtering (strict mode)..."

# Reset ufw to clean state
sudo ufw --force reset > /dev/null 2>&1

# Default policy: deny all outgoing, allow incoming (for responses)
sudo ufw default deny outgoing
sudo ufw default allow incoming

# Always allow localhost
sudo ufw allow out on lo
sudo ufw allow in on lo

# Allow DNS (required for domain resolution)
sudo ufw allow out 53/udp  # DNS queries
sudo ufw allow out 53/tcp  # DNS over TCP

# Parse and allow IP addresses/CIDRs
if [ -n "$NETWORK_ALLOWED_IPS" ]; then
    IFS=',' read -ra IPS <<< "$NETWORK_ALLOWED_IPS"
    for ip in "${IPS[@]}"; do
        # Trim whitespace
        ip=$(echo "$ip" | xargs)
        if [ -n "$ip" ]; then
            echo "  ✓ Allowing IP: $ip"
            sudo ufw allow out to "$ip"
        fi
    done
fi

# Parse and block IP addresses/CIDRs (takes precedence)
if [ -n "$NETWORK_BLOCKED_IPS" ]; then
    IFS=',' read -ra IPS <<< "$NETWORK_BLOCKED_IPS"
    for ip in "${IPS[@]}"; do
        ip=$(echo "$ip" | xargs)
        if [ -n "$ip" ]; then
            echo "  ✗ Blocking IP: $ip"
            sudo ufw deny out to "$ip"
        fi
    done
fi

# Resolve and allow domains
# Note: Wildcard domains like *.anthropic.com need DNS resolution
if [ -n "$NETWORK_ALLOWED_DOMAINS" ]; then
    IFS=',' read -ra DOMAINS <<< "$NETWORK_ALLOWED_DOMAINS"
    for domain in "${DOMAINS[@]}"; do
        domain=$(echo "$domain" | xargs)
        if [ -n "$domain" ]; then
            # Remove wildcard prefix if present
            clean_domain="${domain#\*.}"

            echo "  ✓ Allowing domain: $domain"

            # Resolve domain to IPs (both IPv4 and IPv6)
            # This is a simple approach - resolves at container start
            ips=$(dig +short "$clean_domain" A "$clean_domain" AAAA 2>/dev/null | grep -E '^[0-9a-f.:]+$' || true)

            if [ -n "$ips" ]; then
                while IFS= read -r ip; do
                    if [ -n "$ip" ]; then
                        sudo ufw allow out to "$ip" > /dev/null 2>&1
                    fi
                done <<< "$ips"
            fi
        fi
    done
fi

# Block domains (resolve and deny)
if [ -n "$NETWORK_BLOCKED_DOMAINS" ]; then
    IFS=',' read -ra DOMAINS <<< "$NETWORK_BLOCKED_DOMAINS"
    for domain in "${DOMAINS[@]}"; do
        domain=$(echo "$domain" | xargs)
        if [ -n "$domain" ]; then
            clean_domain="${domain#\*.}"

            echo "  ✗ Blocking domain: $domain"

            ips=$(dig +short "$clean_domain" A "$clean_domain" AAAA 2>/dev/null | grep -E '^[0-9a-f.:]+$' || true)

            if [ -n "$ips" ]; then
                while IFS= read -r ip; do
                    if [ -n "$ip" ]; then
                        sudo ufw deny out to "$ip" > /dev/null 2>&1
                    fi
                done <<< "$ips"
            fi
        fi
    done
fi

# Enable firewall
sudo ufw --force enable > /dev/null 2>&1

echo "✅ Network filtering configured"
echo ""

# Runtime rule management functions (called via docker exec)

add_allow_rule() {
    local ip=$1
    if [ -z "$ip" ]; then
        echo "Error: IP address required"
        return 1
    fi

    echo "Adding allow rule for: $ip"
    sudo ufw allow out to "$ip" > /dev/null 2>&1
    echo "✓ Rule added"
}

add_deny_rule() {
    local ip=$1
    if [ -z "$ip" ]; then
        echo "Error: IP address required"
        return 1
    fi

    echo "Adding deny rule for: $ip"
    sudo ufw deny out to "$ip" > /dev/null 2>&1
    echo "✓ Rule added"
}

remove_rule() {
    local ip=$1
    if [ -z "$ip" ]; then
        echo "Error: IP address required"
        return 1
    fi

    echo "Removing rules for: $ip"
    local rule_nums
    rule_nums=$(sudo ufw status numbered | grep "$ip" | sed -E 's/\[([0-9]+)\].*/\1/' | sort -rn)

    if [ -z "$rule_nums" ]; then
        echo "No rules found for $ip"
        return 0
    fi

    for num in $rule_nums; do
        sudo ufw --force delete "$num" > /dev/null 2>&1
    done

    echo "✓ Rules removed"
}

show_status() {
    echo "=== UFW Status ===="
    sudo ufw status numbered
}

# Runtime mode detection
if [ "$1" = "add_allow_rule" ]; then
    add_allow_rule "$2"
    exit $?
elif [ "$1" = "add_deny_rule" ]; then
    add_deny_rule "$2"
    exit $?
elif [ "$1" = "remove_rule" ]; then
    remove_rule "$2"
    exit $?
elif [ "$1" = "show_status" ]; then
    show_status
    exit $?
fi

# Default: startup initialization (existing code runs)
