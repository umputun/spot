package runner

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/spot/pkg/config"
	"github.com/umputun/spot/pkg/executor"
	"github.com/umputun/spot/pkg/runner/mocks"
)

func Test_templaterVars(t *testing.T) {
	tm := templater{
		hostAddr: "example.com",
		hostName: "example",
		command:  "ls",
		task:     &config.Task{Name: "deploy", User: "user"},
		env:      map[string]string{"SPOT_TASK": "shadowed", "MY_VAR": "__SQ__:my$val"},
	}
	vars := tm.vars()
	assert.Equal(t, "deploy", vars["SPOT_TASK"], "env must not shadow a built-in")
	assert.Equal(t, "example.com", vars["SPOT_REMOTE_HOST"])
	assert.Equal(t, "my$val", vars["MY_VAR"], "SQ marker must be stripped")
}

func Test_templateMatchesRemote(t *testing.T) {
	rendered := []byte("hello")
	renderedSum := fmt.Sprintf("%x", sha256.Sum256(rendered))

	t.Run("matches content and mode with rc noise on stdout", func(t *testing.T) {
		var commands []string
		mock := &mocks.InterfaceMock{
			RunFunc: func(_ context.Context, c string, _ *executor.RunOpts) ([]string, error) {
				commands = append(commands, c)
				switch {
				case strings.Contains(c, "sha256sum"):
					return []string{"> motd", "spot-sum " + renderedSum}, nil
				case strings.Contains(c, "stat -c"):
					return []string{"spot-mode 4755"}, nil
				}
				return nil, fmt.Errorf("unexpected probe %q", c)
			},
		}
		ec := execCmd{exec: mock, cmd: config.Cmd{}}
		assert.True(t, ec.templateMatchesRemote(context.Background(), rendered, "/tmp/out", 0o4755))
		require.Len(t, commands, 2)
		// each probe is one wrapped shell command that falls back to the bsd spelling
		assert.Contains(t, commands[0], "sha256sum")
		assert.Contains(t, commands[0], "shasum -a 256")
		assert.Contains(t, commands[1], "stat -c %a")
		assert.Contains(t, commands[1], "stat -f %Lp")
	})

	t.Run("content differs", func(t *testing.T) {
		mock := &mocks.InterfaceMock{
			RunFunc: func(_ context.Context, _ string, _ *executor.RunOpts) ([]string, error) {
				return []string{"spot-sum " + strings.Repeat("0", 64)}, nil
			},
		}
		ec := execCmd{exec: mock, cmd: config.Cmd{}}
		assert.False(t, ec.templateMatchesRemote(context.Background(), rendered, "/tmp/out", 0o600))
	})

	t.Run("mode differs", func(t *testing.T) {
		mock := &mocks.InterfaceMock{
			RunFunc: func(_ context.Context, c string, _ *executor.RunOpts) ([]string, error) {
				switch {
				case strings.Contains(c, "sha256sum"):
					return []string{"spot-sum " + renderedSum}, nil
				case strings.Contains(c, "stat -c"):
					return []string{"spot-mode 600"}, nil
				}
				return nil, fmt.Errorf("unexpected probe %q", c)
			},
		}
		ec := execCmd{exec: mock, cmd: config.Cmd{}}
		assert.False(t, ec.templateMatchesRemote(context.Background(), rendered, "/tmp/out", 0o644))
	})

	t.Run("missing remote file emits no tagged line", func(t *testing.T) {
		mock := &mocks.InterfaceMock{
			RunFunc: func(_ context.Context, _ string, _ *executor.RunOpts) ([]string, error) {
				return nil, nil
			},
		}
		ec := execCmd{exec: mock, cmd: config.Cmd{}}
		assert.False(t, ec.templateMatchesRemote(context.Background(), rendered, "/tmp/out", 0o600))
	})

	t.Run("bsd mode spelling with leading zero parses", func(t *testing.T) {
		mock := &mocks.InterfaceMock{
			RunFunc: func(_ context.Context, c string, _ *executor.RunOpts) ([]string, error) {
				switch {
				case strings.Contains(c, "sha256sum"):
					return []string{"spot-sum " + renderedSum}, nil
				case strings.Contains(c, "stat -c"):
					return []string{"spot-mode 0644"}, nil
				}
				return nil, fmt.Errorf("unexpected probe %q", c)
			},
		}
		ec := execCmd{exec: mock, cmd: config.Cmd{}}
		assert.True(t, ec.templateMatchesRemote(context.Background(), rendered, "/tmp/out", 0o644))
	})
}

func Test_execTemplateForcesUploadAfterIdempotenceMiss(t *testing.T) {
	src := filepath.Join(t.TempDir(), "spot-template-force.tmpl")
	require.NoError(t, os.WriteFile(src, []byte("task={{.SPOT_TASK}}"), 0o600))

	var uploadOpts *executor.UpDownOpts
	mock := &mocks.InterfaceMock{
		RunFunc: func(_ context.Context, _ string, _ *executor.RunOpts) ([]string, error) {
			return nil, nil
		},
		UploadFunc: func(_ context.Context, _ string, _ string, opts *executor.UpDownOpts) error {
			uploadOpts = opts
			return nil
		},
	}

	ec := execCmd{
		exec:     mock,
		tsk:      &config.Task{Name: "task1", User: "deploy"},
		cmd:      config.Cmd{Name: "render", Template: config.TemplateInternal{Source: src, Dest: "/tmp/out"}},
		hostAddr: "example.com:22",
		hostName: "myhost",
	}

	_, err := ec.Template(context.Background())
	require.NoError(t, err)
	require.NotNil(t, uploadOpts)
	assert.True(t, uploadOpts.Force)
}

func Test_execTemplate(t *testing.T) {
	testingHostAndPort, teardown := startTestContainer(t)
	defer teardown()
	logs := executor.MakeLogs(false, false, nil)
	ctx := context.Background()
	connector, connErr := executor.NewConnector("testdata/test_ssh_key", time.Second*10, logs)
	require.NoError(t, connErr)
	sess, errSess := connector.Connect(ctx, testingHostAndPort, "my-hostAddr", "test")
	require.NoError(t, errSess)

	cleanup := func(path string) {
		sess.Run(ctx, "rm -f "+path, nil)      // nolint
		sess.Run(ctx, "sudo rm -f "+path, nil) // nolint
	}

	testHost, testPort, _ := net.SplitHostPort(testingHostAndPort)
	makeEC := func(cmd config.Cmd) execCmd {
		return execCmd{
			exec:     sess,
			tsk:      &config.Task{Name: "task1", User: "deploy"},
			cmd:      cmd,
			hostAddr: testingHostAndPort,
			hostName: "myhost",
		}
	}

	t.Run("basic template render and upload", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_basic_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		ec := makeEC(config.Cmd{
			Name:     "render basic",
			Template: config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, " {template:")
		assert.Contains(t, resp.details, dst)

		out, err := sess.Run(ctx, "cat "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"hello, " + testingHostAndPort,
			"port=" + testPort,
			"user=deploy",
			"task=task1",
			"name=myhost",
			"cmd=render basic",
		}, out)
	})

	t.Run("template with environment and secrets", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_env_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		ec := makeEC(config.Cmd{
			Name:        "render with env",
			Template:    config.TemplateInternal{Source: "testdata/template_env.tmpl", Dest: dst},
			Environment: map[string]string{"GREETING": "hi-from-env"},
			Secrets:     map[string]string{"MY_SECRET": "s3cret-value"},
			Options:     config.CmdOptions{Secrets: []string{"MY_SECRET"}},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, " {template:")

		out, err := sess.Run(ctx, "cat "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"hello, " + testingHostAndPort,
			"port=" + testPort,
			"user=deploy",
			"task=task1",
			"name=myhost",
			"cmd=render with env",
			"msg=hi-from-env",
			"secret=s3cret-value",
		}, out)
	})

	t.Run("secret cannot shadow a SPOT built-in", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_secret_shadow_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		ec := makeEC(config.Cmd{
			Name:     "render secret shadow",
			Template: config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst},
			Secrets:  map[string]string{"SPOT_TASK": "intruder"},
			Options:  config.CmdOptions{Secrets: []string{"SPOT_TASK"}},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, " {template:")

		out, err := sess.Run(ctx, "cat "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "task=task1", out[3])
	})

	t.Run("template with single-quoted env var", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_sq_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		ec := makeEC(config.Cmd{
			Name:        "render sq",
			Template:    config.TemplateInternal{Source: "testdata/template_env.tmpl", Dest: dst},
			Environment: map[string]string{"GREETING": "__SQ__:hi-from-sq-env", "MY_SECRET": ""},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, " {template:")

		out, err := sess.Run(ctx, "cat "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "msg=hi-from-sq-env", out[6])
	})

	t.Run("template exposes SPOT_ERROR", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_error_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		ec := makeEC(config.Cmd{
			Name:     "render error",
			Template: config.TemplateInternal{Source: "testdata/template_error.tmpl", Dest: dst},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, " {template:")

		out, err := sess.Run(ctx, "cat "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{"error="}, out)
	})

	t.Run("template with mkdir, force, chmod+x", func(t *testing.T) {
		testDir := fmt.Sprintf("/tmp/spot_template_mkdir_%d", time.Now().UnixNano())
		dst := testDir + "/script.sh"
		defer sess.Run(ctx, "rm -rf "+testDir, nil) // nolint

		ec := makeEC(config.Cmd{
			Name:     "render script",
			Template: config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst, Mkdir: true, Force: true, ChmodX: true},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "chmod: +x")

		// verify file is executable
		out, err := sess.Run(ctx, "ls -la "+dst, nil)
		require.NoError(t, err)
		assert.Regexp(t, "-rwx[r-][w-]x[r-][w-]x", out[0])
	})

	t.Run("template with sudo", func(t *testing.T) {
		testDir := fmt.Sprintf("/srv/spot_template_sudo_%d", time.Now().UnixNano())
		dst := testDir + "/conf.txt"
		defer sess.Run(ctx, "sudo rm -rf "+testDir, nil) // nolint

		ec := makeEC(config.Cmd{
			Name:     "render with sudo",
			Template: config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst, Mkdir: true},
			Options:  config.CmdOptions{Sudo: true},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "sudo: true")

		// second render with identical content must skip, even with sudo
		resp, err = ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "skip: identical")

		out, err := sess.Run(ctx, "sudo cat "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "hello, "+testingHostAndPort, out[0])
	})

	t.Run("template with setuid mode", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_setuid_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		ec := makeEC(config.Cmd{
			Name:     "render setuid",
			Template: config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst, Mode: "4755"},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, " {template:")

		// the setuid bit must survive, not be masked to 0755
		out, err := sess.Run(ctx, "stat -c %a "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "4755", out[0])

		// second render with identical content and mode must skip
		resp, err = ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "skip: identical")
	})

	t.Run("template file not found", func(t *testing.T) {
		ec := makeEC(config.Cmd{
			Name:     "render missing",
			Template: config.TemplateInternal{Source: "testdata/does_not_exist.tmpl", Dest: "/tmp/nope.txt"},
		})
		_, err := ec.Template(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can't read template")
	})

	t.Run("template syntax error", func(t *testing.T) {
		badPath := filepath.Join(t.TempDir(), "bad.tmpl")
		require.NoError(t, os.WriteFile(badPath, []byte("hello {{ .X"), 0o600))

		ec := makeEC(config.Cmd{
			Name:     "render bad",
			Template: config.TemplateInternal{Source: badPath, Dest: "/tmp/nope.txt"},
		})
		_, err := ec.Template(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can't parse template")
	})

	t.Run("template condition skip", func(t *testing.T) {
		ec := makeEC(config.Cmd{
			Name:      "render with cond",
			Template:  config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: "/tmp/should_not_exist.txt"},
			Condition: "test -f /tmp/never_existing_condition_file",
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Equal(t, " {skip: render with cond}", resp.details)
	})

	t.Run("template condition passes", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_cond_pass_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		ec := makeEC(config.Cmd{
			Name:      "render with cond pass",
			Template:  config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst},
			Condition: "test -d /tmp",
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, " {template:")

		out, err := sess.Run(ctx, "cat "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "hello, "+testingHostAndPort, out[0])
	})

	t.Run("template undefined key fails", func(t *testing.T) {
		badPath := filepath.Join(t.TempDir(), "undefined.tmpl")
		require.NoError(t, os.WriteFile(badPath, []byte("hello {{.UNDEFINED_VAR}}"), 0o600))

		ec := makeEC(config.Cmd{
			Name:     "render undefined",
			Template: config.TemplateInternal{Source: badPath, Dest: "/tmp/nope.txt"},
		})
		_, err := ec.Template(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "can't execute template")
		assert.Contains(t, err.Error(), "UNDEFINED_VAR")
	})

	t.Run("template with var substitution in dst path", func(t *testing.T) {
		base := fmt.Sprintf("/tmp/spot_template_vardst_%d", time.Now().UnixNano())
		dst := base + "/{SPOT_REMOTE_ADDR}/config.txt"
		expectedFile := base + "/" + testHost + "/config.txt"
		defer sess.Run(ctx, "rm -rf "+base, nil) // nolint

		ec := makeEC(config.Cmd{
			Name:     "render dst vars",
			Template: config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst, Mkdir: true},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, " {template:")

		out, err := sess.Run(ctx, "cat "+expectedFile, nil)
		require.NoError(t, err)
		assert.Equal(t, "hello, "+testingHostAndPort, out[0])
	})

	t.Run("template with ipv6 host", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_ipv6_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		ec := execCmd{
			exec: sess, tsk: &config.Task{Name: "task1", User: "deploy"},
			cmd: config.Cmd{
				Name:     "render ipv6",
				Template: config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst},
			},
			hostAddr: "[2001:db8::1]:2222",
			hostName: "ipv6host",
		}
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, " {template:")

		out, err := sess.Run(ctx, "cat "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"hello, [2001:db8::1]:2222",
			"port=2222",
			"user=deploy",
			"task=task1",
			"name=ipv6host",
			"cmd=render ipv6",
		}, out)
	})

	t.Run("template with mode", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_mode_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		ec := makeEC(config.Cmd{
			Name:     "render mode",
			Template: config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst, Mode: "0644"},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, " {template:")

		out, err := sess.Run(ctx, "stat -c %a "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "644", out[0])

		// verify content is correct
		out, err = sess.Run(ctx, "cat "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "hello, "+testingHostAndPort, out[0])
	})

	t.Run("register vars from script into template", func(t *testing.T) {
		scriptEC := makeEC(config.Cmd{
			Name:     "register vars",
			Script:   "export MY_VAR='my$special@value'",
			Register: []string{"MY_VAR"},
		})
		resp, err := scriptEC.Script(ctx)
		require.NoError(t, err)

		val, ok := resp.registered["MY_VAR"]
		require.True(t, ok)
		assert.True(t, strings.HasPrefix(val, "__SQ__:"), "should have SQ marker")

		dst := fmt.Sprintf("/tmp/spot_template_reg_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		tmplPath := filepath.Join(t.TempDir(), "reg_var.tmpl")
		require.NoError(t, os.WriteFile(tmplPath, []byte("value={{.MY_VAR}}"), 0o644))

		tc := makeEC(config.Cmd{
			Name:        "render with registered var",
			Template:    config.TemplateInternal{Source: tmplPath, Dest: dst},
			Environment: map[string]string{"MY_VAR": val},
		})
		tresp, err := tc.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, tresp.details, " {template:")

		out, err := sess.Run(ctx, "cat "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "value=my$special@value", strings.Join(out, "\n"))
	})

	t.Run("template force=false duplicates are skipped", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_force_false_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		ec := makeEC(config.Cmd{
			Name:     "render first",
			Template: config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, " {template:")
		assert.NotContains(t, resp.details, "skip")

		// second render with identical content and force=false must be skipped, not re-uploaded
		resp, err = ec.Template(ctx)
		require.NoError(t, err)
		assert.Equal(t, fmt.Sprintf(" {template: testdata/template_basic.tmpl -> %s, skip: identical}", dst), resp.details)

		// verify content is correct after both renders
		out, err := sess.Run(ctx, "cat "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"hello, " + testingHostAndPort,
			"port=" + testPort,
			"user=deploy",
			"task=task1",
			"name=myhost",
			"cmd=render first",
		}, out)
	})

	t.Run("template changed content is re-uploaded", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_changed_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		// first render with one env value
		ec := makeEC(config.Cmd{
			Name:        "render changed",
			Template:    config.TemplateInternal{Source: "testdata/template_env.tmpl", Dest: dst},
			Environment: map[string]string{"GREETING": "first"},
			Secrets:     map[string]string{"MY_SECRET": "s3cret"},
			Options:     config.CmdOptions{Secrets: []string{"MY_SECRET"}},
		})
		_, err := ec.Template(ctx)
		require.NoError(t, err)

		// second render with a different env value, same dst, force=false: must upload, not skip
		ec.cmd.Environment = map[string]string{"GREETING": "second"}
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.NotContains(t, resp.details, "skip")

		out, err := sess.Run(ctx, "cat "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "msg=second", out[6])
	})

	t.Run("template chmod+x is idempotent under force=false", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_x_idem_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		ec := makeEC(config.Cmd{
			Name:     "render chmod+x",
			Template: config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst, ChmodX: true},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "chmod: +x")
		assert.NotContains(t, resp.details, "skip")

		// second render: execute bits are part of the wanted mode, so the run must skip
		resp, err = ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, "skip: identical")

		out, err := sess.Run(ctx, "stat -c %a "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "711", out[0])
	})

	t.Run("template chmod+x with explicit mode", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_mode_x_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		ec := makeEC(config.Cmd{
			Name:     "render mode x",
			Template: config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst, Mode: "0644", ChmodX: true},
		})
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.Contains(t, resp.details, " {template:")

		// mode 0644 with chmod+x folds to 0755
		out, err := sess.Run(ctx, "stat -c %a "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "755", out[0])
	})

	t.Run("template mode change re-applies mode without content change", func(t *testing.T) {
		dst := fmt.Sprintf("/tmp/spot_template_mode_change_%d.txt", time.Now().UnixNano())
		defer cleanup(dst)

		mk := func(mode string) execCmd {
			return makeEC(config.Cmd{
				Name:     "render mode change",
				Template: config.TemplateInternal{Source: "testdata/template_basic.tmpl", Dest: dst, Mode: mode},
			})
		}

		ec := mk("0644")
		_, err := ec.Template(ctx)
		require.NoError(t, err)
		out, err := sess.Run(ctx, "stat -c %a "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "644", out[0])

		// same content, different mode: second run must fix the mode, not skip
		ec = mk("0600")
		resp, err := ec.Template(ctx)
		require.NoError(t, err)
		assert.NotContains(t, resp.details, "skip")
		out, err = sess.Run(ctx, "stat -c %a "+dst, nil)
		require.NoError(t, err)
		assert.Equal(t, "600", out[0])
	})
}
