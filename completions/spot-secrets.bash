# bash completion for spot-secrets (generated via go-flags)
_spot_secrets() {
    local args=("${COMP_WORDS[@]:1:$COMP_CWORD}")
    local IFS=$'\n'
    COMPREPLY=($(GO_FLAGS_COMPLETION=1 "${COMP_WORDS[0]}" "${args[@]}" 2>/dev/null))
    return 0
}
complete -o default -F _spot_secrets spot-secrets
