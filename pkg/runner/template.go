package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/umputun/spot/pkg/config"
	"github.com/umputun/spot/pkg/executor"
)

// Template renders a local Go text/template file with the command environment and SPOT_* variables,
// then uploads the result to a remote host. It supports mkdir, force, chmod+x and mode options, and
// works with sudo the same way as the copy command.
func (ec *execCmd) Template(ctx context.Context) (resp execCmdResp, err error) {
	cond, err := ec.checkCondition(ctx)
	if err != nil {
		return resp, err
	}
	if !cond {
		resp.details = fmt.Sprintf(" {skip: %s}", ec.cmd.Name)
		return resp, nil
	}

	tmpl := templater{hostAddr: ec.hostAddr, hostName: ec.hostName, task: ec.tsk, command: ec.cmd.Name, env: ec.cmd.Environment}
	src := tmpl.apply(ec.cmd.Template.Source)
	dst := tmpl.apply(ec.cmd.Template.Dest)

	rendered, err := ec.renderTemplate(tmpl, src)
	if err != nil {
		return resp, err
	}

	mode, err := ec.templateMode()
	if err != nil {
		return resp, err
	}

	if !ec.cmd.Template.Force {
		if ec.templateMatchesRemote(ctx, rendered, dst, mode) {
			resp.details = fmt.Sprintf(" {template: %s -> %s, skip: identical}", src, dst)
			return resp, nil
		}
	}

	if err := ec.uploadRenderedTemplate(ctx, rendered, dst); err != nil {
		return resp, err
	}
	if err := ec.applyTemplateMode(ctx, dst, mode); err != nil {
		return resp, err
	}

	resp.details = fmt.Sprintf(" {template: %s -> %s}", src, dst)
	if ec.cmd.Options.Sudo {
		resp.details += ", sudo: true"
	}
	if ec.cmd.Template.ChmodX {
		resp.details += ", chmod: +x"
	}
	return resp, nil
}

// renderTemplate parses and executes the template file, returning the rendered bytes.
func (ec *execCmd) renderTemplate(tmpl templater, src string) ([]byte, error) {
	data, err := os.ReadFile(src) // nolint:gosec // user-configured template path
	if err != nil {
		return nil, ec.errorFmt("can't read template %q: %w", src, err)
	}

	tplData := tmpl.vars()
	for _, k := range ec.cmd.Options.Secrets {
		if _, ok := tplData[k]; ok {
			continue // built-ins and env win over secret keys, same precedence as vars()
		}
		if v, ok := ec.cmd.Secrets[k]; ok {
			tplData[k] = v
		}
	}

	parsed, err := template.New(filepath.Base(src)).Option("missingkey=error").Parse(string(data))
	if err != nil {
		return nil, ec.errorFmt("can't parse template %q: %w", src, err)
	}
	var rendered bytes.Buffer
	if err = parsed.Execute(&rendered, tplData); err != nil {
		return nil, ec.errorFmt("can't execute template %q: %w", src, err)
	}
	return rendered.Bytes(), nil
}

// templateMode resolves the wanted destination mode: explicit mode or 0600 default, chmod+x adds
// the execute bits.
func (ec *execCmd) templateMode() (os.FileMode, error) {
	modeStr := ec.cmd.Template.Mode
	if modeStr == "" {
		modeStr = "0600"
	}
	modeVal, err := strconv.ParseUint(modeStr, 8, 32)
	if err != nil {
		return 0, ec.errorFmt("can't parse mode %q: %w", modeStr, err)
	}
	mode := os.FileMode(modeVal)
	if ec.cmd.Template.ChmodX {
		mode |= 0o111
	}
	return mode, nil
}

// uploadRenderedTemplate writes the render to a staging file and reuses copyPush for the upload.
// the staging file stays 0600 so a secret render is never widened in temp; the wanted mode is
// applied to dst after the upload. force is on unconditionally: templateMatchesRemote has already
// decided the render differs, so copyPush must not skip it by metadata.
func (ec *execCmd) uploadRenderedTemplate(ctx context.Context, rendered []byte, dst string) error {
	tmp, err := os.CreateTemp("", "spot-template")
	if err != nil {
		return ec.errorFmt("can't create temp file for rendered template: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if rErr := os.Remove(tmpName); rErr != nil {
			log.Printf("[WARN] can't remove temp template file %s: %v", tmpName, rErr)
		}
	}()
	if _, err = tmp.Write(rendered); err != nil {
		_ = tmp.Close()
		return ec.errorFmt("can't write rendered template to temp file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return ec.errorFmt("can't close temp template file: %w", err)
	}

	ecCopy := *ec
	ecCopy.cmd.Copy = config.CopyInternal{Mkdir: ec.cmd.Template.Mkdir, Force: true}
	if _, err := ecCopy.copyPush(ctx, tmpName, dst); err != nil {
		return err
	}
	return nil
}

// applyTemplateMode applies the wanted mode on dst. on Windows the staging stat does not carry unix
// perms, so the chmod always runs. the full mode value is used so setuid/setgid/sticky bits survive.
func (ec *execCmd) applyTemplateMode(ctx context.Context, dst string, mode os.FileMode) error {
	chmodCmd := ec.wrapWithSudo(fmt.Sprintf("chmod %o %s", mode, shellQuote(dst)))
	if _, err := ec.exec.Run(ctx, chmodCmd, discardOutput); err != nil {
		return ec.errorFmt("can't chmod %s on %s: %w", dst, ec.hostAddr, err)
	}
	return nil
}

// templateMatchesRemote reports whether dst already has the rendered content and the wanted mode.
// a missing or unreadable remote file counts as a mismatch so the caller uploads. the probes use the
// GNU spelling first and the BSD/macOS spelling as fallback, so idempotence works on non-GNU hosts.
func (ec *execCmd) templateMatchesRemote(ctx context.Context, rendered []byte, dst string, mode os.FileMode) bool {
	qDst := shellQuote(dst)

	renderedSum := fmt.Sprintf("%x", sha256.Sum256(rendered))
	sumProbe := fmt.Sprintf(
		"(sha256sum %s 2>/dev/null || shasum -a 256 %s 2>/dev/null) | awk '{print \"spot-sum \"$1}'", qDst, qDst)
	sumLine, ok := ec.runTemplateProbe(ctx, sumProbe, "spot-sum ")
	if !ok || !strings.EqualFold(sumLine, renderedSum) {
		return false
	}

	modeProbe := fmt.Sprintf(
		"(stat -L -c %%a %s 2>/dev/null || stat -L -f %%Mp%%Lp %s 2>/dev/null) | awk '{print \"spot-mode \"$1}'", qDst, qDst)
	modeLine, ok := ec.runTemplateProbe(ctx, modeProbe, "spot-mode ")
	if !ok {
		return false
	}
	// GNU prints 755, BSD prints 0755; parse as uint so both compare the same against the full mode
	remoteMode, err := strconv.ParseUint(modeLine, 8, 32)
	if err != nil {
		return false
	}
	return uint32(remoteMode) == uint32(mode)
}

// runTemplateProbe runs a single shell command and returns the value after tag in its output line.
// the tagged line makes the scan immune to shell rc noise on stdout, which a bare out[0] read would
// catch as the wrong field. the whole alternation is wrapped once so sudo covers both branches.
// the non-sudo path is prefixed with exec so the local executor cannot strip the /bin/sh -c wrapper
// and unbalance the probe's single-quote escapes.
func (ec *execCmd) runTemplateProbe(ctx context.Context, probe, tag string) (string, bool) {
	c := "exec " + ec.shell() + " -c " + shellQuote(probe)
	if ec.cmd.Options.Sudo {
		c = ec.wrapWithSudo(ec.shell() + " -c " + shellQuote(probe))
	}
	keep := func(line string) bool { return strings.HasPrefix(line, tag) }
	out, err := ec.exec.Run(ctx, c, &executor.RunOpts{KeepLine: keep})
	if err != nil {
		log.Printf("[DEBUG] can't probe remote file, will re-upload: %v", err)
		return "", false
	}
	for _, line := range out {
		if v, found := strings.CutPrefix(line, tag); found {
			return strings.TrimSpace(v), true
		}
	}
	log.Printf("[DEBUG] remote probe emitted no %q line, will re-upload", tag)
	return "", false
}

// shellQuote wraps a path for safe use in a shell command, escaping embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
