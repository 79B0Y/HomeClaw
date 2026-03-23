#!/bin/bash

# LinknLink Lightweight Text Display Script
# Zero dependencies, pure Bash implementation

set -e

# Configuration
TTY_DEVICE="${TTY_DEVICE:-/dev/tty1}"
REFRESH_INTERVAL="${REFRESH_INTERVAL:-5}"
ENABLE_COLOR="${ENABLE_COLOR:-true}"

# Color Definitions
if [ "$ENABLE_COLOR" = "true" ]; then
    RESET='\033[0m'
    BOLD='\033[1m'
    CYAN='\033[36m'
    GREEN='\033[32m'
else
    RESET=''
    BOLD=''
    CYAN=''
    GREEN=''
fi

# Log function (disabled)
log() {
    :
}

# Get IP address
get_ip() {
    hostname -I | awk '{print $1}' | grep -oE '^[0-9.]+$' || echo "No IP"
}

# Get hostname
get_hostname() {
    hostname
}

# Get current time
get_time() {
    date '+%Y-%m-%d %H:%M:%S'
}

# Generate display content
generate_display() {
    local hostname="$1"
    local ip="$2"
    local time="$3"
    
    clear
    
    # Top border (80 chars wide)
    echo -e "${CYAN}╔════════════════════════════════════════════════════════════════════════════════╗${RESET}"
    echo -e "${CYAN}║${RESET}                                                                              ${CYAN}║${RESET}"
    
    # Logo (full width)
    echo -e "${CYAN}║${RESET}  ██╗     ██╗███╗   ██╗██╗  ██╗███╗   ██╗██╗     ██╗███╗   ██╗██╗  ██╗    ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ██║     ██║████╗  ██║██║ ██╔╝████╗  ██║██║     ██║████╗  ██║██║ ██╔╝    ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ██║     ██║██╔██╗ ██║█████╔╝ ██╔██╗ ██║██║     ██║██╔██╗ ██║█████╔╝     ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ██║     ██║██║╚██╗██║██╔═██╗ ██║╚██╗██║██║     ██║██║╚██╗██║██╔═██╗     ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ███████╗██║██║ ╚████║██║  ██╗██║ ╚████║███████╗██║██║ ╚████║██║  ██╗    ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ╚══════╝╚═╝╚═╝  ╚═══╝╚═╝  ╚═╝╚═╝  ╚═══╝╚══════╝╚═╝╚═╝  ╚═══╝╚═╝  ╚═╝    ${CYAN}║${RESET}"
    
    echo -e "${CYAN}║${RESET}                                                                              ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}                        ${BOLD}LinknLink HomeClaw${RESET}                     ${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}                                                                              ${CYAN}║${RESET}"
    
    # Middle separator
    echo -e "${CYAN}╠════════════════════════════════════════════════════════════════════════════════╣${RESET}"
    echo -e "${CYAN}║${RESET}                                                                              ${CYAN}║${RESET}"
    
    # Device Information
    echo -e "${CYAN}║${RESET}  ${GREEN}HomePage URL${RESET}: ${BOLD}http://$ip${RESET}$(printf '%*s' $((55 - ${#ip})) '')${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ${GREEN}OpenClaw URL${RESET}: ${BOLD}https://$ip${RESET}$(printf '%*s' $((53 - ${#ip})) '')${CYAN}║${RESET}"
    echo -e "${CYAN}║${RESET}  ${GREEN}Last Update${RESET}: ${BOLD}$time${RESET}$(printf '%*s' $((62 - ${#time})) '')${CYAN}║${RESET}"
    
    echo -e "${CYAN}║${RESET}                                                                              ${CYAN}║${RESET}"
    
    # Bottom border
    echo -e "${CYAN}╚════════════════════════════════════════════════════════════════════════════════╝${RESET}"
}

# Display to TTY
display_to_tty() {
    local content="$1"
    
    if [ ! -e "$TTY_DEVICE" ]; then
        return 1
    fi
    
    # Output to TTY, suppress all errors
    echo "$content" > "$TTY_DEVICE" 2>/dev/null || return 1
}

# Ignore all signals to prevent interruption
trap '' INT TERM QUIT HUP

# Main loop
main() {
    local last_ip=""
    
    while true; do
        local current_ip=$(get_ip 2>/dev/null || echo "No IP")
        local current_hostname=$(get_hostname 2>/dev/null || echo "unknown")
        local current_time=$(get_time 2>/dev/null || echo "")
        
        # Only regenerate display when IP changes
        if [ "$current_ip" != "$last_ip" ]; then
            local display=$(generate_display "$current_hostname" "$current_ip" "$current_time")
            display_to_tty "$display" 2>/dev/null || true
            last_ip="$current_ip"
        fi
        
        sleep "$REFRESH_INTERVAL"
    done
}

# Debug mode: generate display only
if [ "$1" = "--generate-only" ]; then
    generate_display "$(get_hostname)" "$(get_ip)" "$(get_time)" 2>/dev/null
    exit 0
fi

# Start main program, suppress all errors
main 2>/dev/null &
wait
