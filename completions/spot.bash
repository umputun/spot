# bash completion for spot (generated via go-flags)
_spot() {
    local args=("${COMP_WORDS[@]:1:$COMP_CWORD}")
    local IFS=$'\n'
    COMPREPLY=($(GO_FLAGS_COMPLETION=1 "${COMP_WORDS[0]}" "${args[@]}" 2>/dev/null))
    return 0
}
complete -o default -F _spot spot
