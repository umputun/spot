# Spot

Spot (aka `simplotask`) is a powerful and easy-to-use tool for effortless deployment and configuration management. It allows users to define a playbook with the list of tasks and targets, where each task consists of a series of commands that can be executed on remote hosts concurrently. Spot supports running scripts, copying files, syncing directories, and deleting files or directories, as well as custom inventory files or inventory URLs.

<div align="center">
  <img class="logo" src="https://github.com/umputun/simplotask/raw/master/site/spot-bg.png" width="400px" alt="Spot | Effortless Deployment"/>
</div>

## Features

- Define tasks with a list of commands and the list of target hosts.
- Support for remote hosts specified directly or through inventory files/URLs.
- Everything can be defined in a simple YAML or TOML file.
- Run scripts on remote hosts as well as on the localhost.
- Built-in commands: copy, sync, delete and wait.
- Concurrent execution of task on multiple hosts.
- Ability to wait for a specific condition before executing the next command.
- Customizable environment variables.
- Ability to override list of destination hosts, ssh username and ssh key file.
- Skip or execute only specific commands.
- Catch errors and execute a command hook on the local host.
- Debug mode to print out the commands to be executed, output of the commands, and all the other details.
- Dry-run mode to print out the commands to be executed without actually executing them.
- Ad-hoc mode to execute a single command on a list of hosts.
- A single binary with no dependencies.
----

<div align="center">

[![build](https://github.com/umputun/spot/actions/workflows/ci.yml/badge.svg)](https://github.com/umputun/spot/actions/workflows/ci.yml)&nbsp;[![Coverage Status](https://coveralls.io/repos/github/umputun/spot/badge.svg?branch=master)](https://coveralls.io/github/umputun/spot?branch=master)&nbsp;[![Go Report Card](https://goreportcard.com/badge/github.com/umputun/spot)](https://goreportcard.com/report/github.com/umputun/spot)&nbsp;[![Go Reference](https://pkg.go.dev/badge/github.com/umputun/spot.svg)](https://pkg.go.dev/github.com/umputun/spot)&nbsp;[![GitHub release](https://img.shields.io/github/release/umputun/spot.svg)](https://github.com/umputun/spot/releases)
</div>

## Getting Started

- Install Spot by download the latest release from the [Releases](https://github.com/umputun/spot/releases) page.
- Create a configuration file, as shown in the [example below](#full-playbook-example), and save it as `spot.yml`.
- Run Spot using the following command: `spot`. This will execute all the tasks defined in the default `spot.yml` file for the `default` target with a concurrency of 1.
- To execute a specific task, use the `--task` flag: `spot --task=deploy-things`. This will execute only the `deploy-things` task.
- To execute a specific task for a specific target, use the `--task` and `-t` flags: `spot --task=deploy-things -t prod`. This will execute only the `deploy-things` task for the `prod` target.


<details markdown>
  <summary>Screenshots</summary>

- `spot` with playbook `spot.yml`: `spot -p spot.yml -t prod`

![spot-playbook](https://github.com/umputun/spot/raw/master/site/docs/screen-playbook.jpg)

- `spot` with the same playbook in dry mode: `spot -p spot.yml -t prod -v`

![spot-playbook-dry](https://github.com/umputun/spot/raw/master/site/docs/screen-playbook-dry.jpg)

- `spot` ad-hoc command on given hosts: `spot "ls -la /tmp -t dev1.umputun.com -t dev2.umputun.com`

![spot-adhoc](https://github.com/umputun/spot/raw/master/site/docs/screen-adhoc.jpg)
</details>

## Options

Spot supports the following command-line options:

- `-p`, `--playbook=`: Specifies the playbook file to be used. Defaults to `spot.yml`. You can also set the environment
  variable `$SPOT_PLAYBOOK` to define the playbook file path.
- `--task=`: Specifies the task name to execute. The task should be defined in the playbook file.
  If not specified all the tasks will be executed.
- `-t`, `--target=`: Specifies the target name to use for the task execution. The target should be defined in the playbook file and can represent remote hosts, inventory files, or inventory URLs. If not specified the `default` target will be used. User can pass a host name, group name, tag or IP instead of the target name for a quick override. Providing the `-t`, `--target` flag multiple times with different targets sets multiple destination targets or multiple hosts, e.g., `-t prod -t dev` or `-t example1.com -t example2.com`.
- `-c`, `--concurrent=`: Sets the number of concurrent hosts to execute tasks. Defaults to `1`, which means hosts will be handled  sequentially.
- `--timeout`: Sets the SSH timeout. Defaults to `30s`.
- `-i`, `--inventory=`: Specifies the inventory file or url to use for the task execution. Overrides the inventory file defined in the
  playbook file. User can also set the environment variable `$SPOT_INVENTORY` to define the default inventory file path or url.
- `-u`, `--user=`: Specifies the SSH user to use when connecting to remote hosts. Overrides the user defined in the playbook file .
- `-k`, `--key=`: Specifies the SSH key to use when connecting to remote hosts. Overrides the key defined in the playbook file.
- `-s`, `--skip=`: Skips the specified commands during the task execution. Providing the `-s` flag multiple times with different command names skips multiple commands.
- `-o`, `--only=`: Runs only the specified commands during the task execution. Providing the `-o` flag multiple times with different command names runs only multiple commands.
- `-e`, `--env=`: Sets the environment variables to be used during the task execution. Providing the `-e` flag multiple times with different environment variables sets multiple environment variables, e.g., `-e VAR1=VALUE1 -e VAR2=VALUE2`.
- `--dry`: Enables dry-run mode, which prints out the commands to be executed without actually executing them.
- `-v`, `--verbose`: Enables verbose mode, providing more detailed output and error messages during the task execution.
- `--dbg`: Enables debug mode, providing even more detailed output and error messages during the task execution as well as diagnostic messages.
- `-h` `--help`: Displays the help message, listing all available command-line options.

## Full playbook example

```yaml
user: umputun                       # default ssh user. Can be overridden by -u flag or by inventory or host definition
ssh_key: keys/id_rsa                # ssh key
inventory: /etc/spot/inventory.yml  # default inventory file. Can be overridden by --inventory flag

# list of targets, i.e. hosts, inventory files or inventory URLs
targets:
  prod:
    hosts: # list of hosts, user, name and port optional. 
      - {host: "h1.example.com", user: "user2", name: "h1"}
      - {host: "h2.example.com", port: 2222}
  staging:
    groups: ["dev", "staging"] # list of groups from inventory file
  dev:
    names: ["devbox1", "devbox2"] # list of server names from inventory file
  all:
    groups: ["all"] # all hosts from all groups from inventory file

# list of tasks, i.e. commands to execute
tasks:
  - name: deploy-things
    on_error: "curl -s localhost:8080/error?msg={SPOT_ERROR}" # call hook on error
    commands:
      - name: wait
        script: sleep 5s
      
      - name: copy configuration
        copy: {"src": "testdata/conf.yml", "dst": "/tmp/conf.yml", "mkdir": true}

      - name: copy other files
        copy:
          - {"src": "testdata/f1.csv", "dst": "/tmp/things/f1.csv", "recur": true}
          - {"src": "testdata/f2.csv", "dst": "/tmp/things/f2.csv", "recur": true}

      - name: sync things
        sync: {"src": "testdata", "dst": "/tmp/things"}
      
      - name: some command
        script: |
          ls -laR /tmp
          du -hcs /srv
          cat /tmp/conf.yml
          echo all good, 123
      
      - name: delete things
        delete: {"path": "/tmp/things", "recur": true}
      
      - name: show content
        script: ls -laR /tmp

  - name: docker
    commands:
      - name: docker pull and start
        script: |
          docker pull umputun/remark42:latest
          docker stop remark42 || true
          docker rm remark42 || true
          docker run -d --name remark42 -p 8080:8080 umputun/remark42:latest
        env: {FOO: bar, BAR: qux} # set environment variables for the command
      - wait: {cmd: "curl -s localhost:8080/health", timeout: "10s", interval: "1s"} # wait for health check to pass
```

*Alternatively, the playbook can be represented using the TOML format.*

## Simplified playbook example

In some cases the rich syntax of the full playbook is not needed and can felt over-engineered and even overwhelming. For those situations, Spot supports a simplified playbook format, which is easier to read and write, but also more limited in its capabilities.


```yaml
user: umputun                       # default ssh user. Can be overridden by -u flag or by inventory or host definition
ssh_key: keys/id_rsa                # ssh key
inventory: /etc/spot/inventory.yml  # default inventory file. Can be overridden by --inventory flag

target: ["devbox1", "devbox2", "h1.example.com:2222", "h2.example.com"] # list of host names from inventory and direct host ips

# the actual list of commands to execute
task:
  - name: wait
    script: sleep 5s
  
  - name: copy configuration
    copy: {"src": "testdata/conf.yml", "dst": "/tmp/conf.yml", "mkdir": true}
  
  - name: copy other files
    copy: 
      - {"src": "testdata/f1.csv", "dst": "/tmp/things/f1.csv", "recur": true}
      - {"src": "testdata/f2.csv", "dst": "/tmp/things/f2.csv", "recur": true}
  
  - name: sync things
    sync: {"src": "testdata", "dst": "/tmp/things"}
  
  - name: some command
    script: |
      ls -laR /tmp
      du -hcs /srv
      cat /tmp/conf.yml
      echo all good, 123
  
  - name: delete things
    delete: {"path": "/tmp/things", "recur": true}
  
  - name: show content
    script: ls -laR /tmp

  - name: docker pull and start
    script: |
      docker pull umputun/remark42:latest
      docker stop remark42 || true
      docker rm remark42 || true
      docker run -d --name remark42 -p 8080:8080 umputun/remark42:latest
    env: {FOO: bar, BAR: qux} # set environment variables for the command
    
  - wait: {cmd: "curl -s localhost:8080/health", timeout: "10s", interval: "1s"} # wait for health check to pass

```

## Playbook Types

Spot supports two types of playbooks: full and simplified. Both can be represented in either YAML or TOML format. The full playbook is more powerful and flexible but also more verbose and complex. The simplified playbook, on the other hand, is easier to read and write but has more limited capabilities.

Here are the main differences between the two types of playbooks:

- The full playbook supports multiple target sets, while the simplified playbook only supports a single target set. In other words, the full playbook can execute the same set of commands on multiple environments, with each environment defined as a separate target set. The simplified playbook can execute the same set of commands on just one environment.
- The full playbook supports multiple tasks, while the simplified playbook only supports a single task. This means that the full playbook can execute multiple sets of commands, whereas the simplified playbook can only execute one set of commands.
- The full playbook supports various target types, such as `hosts`, `groups`, and `names`, while the simplified playbook only supports a single type, which is a list of names or host addresses. See the [Targets](#targets) section for more details.
- The simplified playbook does not support task-level `on_error`, `user`, and `ssh_key` fields, while the full playbook does. See the [Task details](#task-details) section for more information.

Both types of playbooks support the remaining fields and options.

## Task details

Each task consists of a list of commands that will be executed on the remote host(s). The task can also define the following optional fields:

- `on_error`: specifies the command to execute on the local host (the one running the `spot` command) in case of an error. The command can use the `{SPOT_ERROR}` variable to access the last error message. Example: `on_error: "curl -s localhost:8080/error?msg={SPOT_ERROR}"`
- `user`: specifies the SSH user to use when connecting to remote hosts. Overrides the user defined in the top section of playbook file for the specified task.

*Note: these fields supported in the full playbook type only*

All tasks are executed sequentially one a given host, one after another. If a task fails, the execution of the playbook will stop and the `on_error` command will be executed on the local host, if defined. Every task has to have `name` field defined, which is used to identify the task everywhere. Playbook with missing `name` field will fail to execute immediately. Duplicate task names are not allowed either.

## Command Types

Spot supports the following command types:

- `script`: can be any valid shell script. The script will be executed on the remote host(s) using SSH, inside a shell.
- `copy`: copies a file from the local machine to the remote host(s). Example: `copy: {"src": "testdata/conf.yml", "dst": "/tmp/conf.yml", "mkdir": true}`. If `mkdir` is set to `true` the command will create the destination directory if it doesn't exist, same as `mkdir -p` in bash. Note: `copy` command type supports multiple commands too, the same way as `mcopy` below.
- `mcopy`: copies multiple files from the local machine to the remote host(s). Example: `mcopy: [{"src": "testdata/1.yml", "dst": "/tmp/1.yml", "mkdir": true}, {"src": "testdata/1.txt", "dst": "/tmp/1.txt"}]`. This is just a shortcut for multiple `copy` commands.
- `sync`: syncs directory from the local machine to the remote host(s). Optionally supports deleting files on the remote host(s) that don't exist locally. Example: `sync: {"src": "testdata", "dst": "/tmp/things", "delete": true}`
- `delete`: deletes a file or directory on the remote host(s), optionally can remove recursively. Example: `delete: {"path": "/tmp/things", "recur": true}`
- `wait`: waits for the specified command to finish on the remote host(s) with 0 error code. This command is useful when you need to wait for a service to start before executing the next command. Allows to specify the timeout as well as check interval. Example: `wait: {"cmd": "curl -s --fail localhost:8080", "timeout": "30s", "interval": "1s"}`

### Command options

Each command type supports the following options:

- `ignore_errors`: if set to `true` the command will not fail the task in case of an error.
- `no_auto`: if set to `true` the command will not be executed automatically, but can be executed manually using the `--only` flag.
- `local`: if set to `true` the command will be executed on the local host (the one running the `spot` command) instead of the remote host(s).

example setting `ignore_errors` and `no_auto` options:

```yaml
  commands:
      - name: wait
        script: sleep 5s
        options: {ignore_errors: true, no_auto: true}
```

### Script Execution

Spot allows executing scripts on remote hosts, or locally if `options.local` is set to true. Scripts can be executed in two different ways, depending on whether they are single-line or multi-line scripts.

**Single-line Script Execution**

For single-line scripts, they are executed directly inside the shell with the optional parameters set to the command line. For example:

```yaml
  commands:
      - name: some command
        script: ls -laR /tmp
        env: {FOO: bar, BAR: qux} 
```        
this will be executed as: `FOO='bar' BAR='qux'ls -laR /tmp FOO=bar BAR=qux` inside the shell on the remote host(s), i.e. `sh -c "FOO='bar' BAR='qux'ls -laR /tmp FOO=bar BAR=qux"`.

**Multi-line Script Execution**

For multi-line scripts, Spot creates a temporary script containing all the commands, uploads it to the remote host (or keeps it locally if `options.local` is set to true), and executes the script. Environment variables are set inside the script, allowing the user to create complex scripts that include setting variables, conditionals, loops, and other advanced functionality. Scripts run with "set -e" to fail on error. For example:

```yaml
commands:
  - name: multi_line_script
    script: |
      touch /tmp/file1
      echo "Hello World" > /tmp/file2
      echo "Executing loop..."
      for i in {1..5}; do
        echo "Iteration $i"
      done
      echo "All done! $FOO $BAR
    env: {FOO: bar, BAR: qux}
```

this will create a temporary script on the remote host(s) with the following content and execute it:

```bash
#!/bin/sh
set -e
export FOO='bar'
export BAR='qux'
touch /tmp/file1
echo "Hello World" > /tmp/file2
echo "Executing loop..."
for i in {1..5}; do
  echo "Iteration $i"
done
echo "All done! $FOO $BAR"
```

By using this approach, Spot enables users to write and execute more complex scripts, providing greater flexibility and power in managing remote hosts or local environments.

## Targets

Targets are used to define the remote hosts to execute the tasks on. Targets can be defined in the playbook file or passed as a command-line argument. The following target types are supported:

- `hosts`: a list of destination host names or IP addresses, with optional port and username, to execute the tasks on. Example: `hosts: [{host: "h1.example.com", user: "test", name: "h1}, {host: "h2.example.com", "port": 2222}]`. If no user is specified, the user defined in the top section of the playbook file (or override) will be used. If no port is specified, port 22 will be used.
- `groups`: a list of groups from inventory to use. Example: `groups: ["dev", "staging"}`. Special group `all` combines all the groups.
- `tags`: a list of tags from inventory to use. Example: `tags: ["tag1", "tag2"}`.
- `names`: a list of host names from inventory to use. Example: `names: ["host1", "host2"}`.

All the target types can be combined, i.e. `hosts`, `groups`, `tags`, `hosts` and `names` all can be used together in the same target. To avoid possible duplicates, the final list of hosts is deduplicated by the host+ip+user. 

example of targets in the playbook file:

```yaml
targets:
  prod:
    hosts: [{host: "h1.example.com", user: "test"}, {"h2.example.com", "port": 2222, name: "h2"}]
  staging:
    groups: ["staging"]
  dev:
    groups: ["dev", "staging"]
    names: ["host1", "host2"]
  all-servers:
    groups: ["all"]
```

*Note: All the target types available in the full playbook file only. The simplified playbook file only supports a single, anonymous target type combining `hosts` and `names` together.*

```yaml
targets: ["host1", "host2", "host3.example.com", "host4.example.com:2222"]
```

in this example, the playbook will be executed on hosts named `host1` and `host2` from the inventory and on hosts `host3.example.com` with port `22` and `host4.example.com` with port `2222`.

### Target overrides

There are several ways to override or alter the target defined in the playbook file via command-line arguments:

- `--inventory` set hosts from the provided inventory file or url. Example: `--inventory=inventory.yml` or `--inventory=http://localhost:8080/inventory`.
- `--target` set groups, names, tags from inventory or directly hosts to run playbook on. Example: `--target=prod` (will run on all hosts in group `prod`) or `--target=example.com:2222` (will run on host `example.com` with port `2222`).
- `--user` set the ssh user to run the playbook on remote hosts. Example: `--user=test`.
- `--key` set the ssh key to run the playbook on remote hosts. Example: `--key=/path/to/key`.

### Target selection

The target selection is done in the following order:

- if `--target` is set, it will be used.
  - first Spot will try to match on target name in the playbook file.
  - if no match found, Spot will try to match on group name in the inventory file.
  - if no match found, Spot will try to match on tags in the inventory file.
  - if no match found, Spot will try to match on host name in the inventory file.
  - if no match found, Spot will try to match on host address in the playbook file.
  - if no match found, Spot will use it as a host address.
- if `--target` is not discovered, Spot will assume the `default` target.

### Inventory 

The inventory file is a simple yml (ot toml) what can represent a list of hosts or a list of groups with hosts. In case if both groups and hosts defined, the hosts will be merged with groups and will add a new group named `hosts`.

By default, inventory loaded from the file/url set in `SPOT_INVENTORY` environment variable. This is the lowest priority and can be overridden by `inventory` from the playbook (next priority) and `--inventory` flag (highest priority)
. 
This is an example of the inventory file with groups

```yaml
groups:
  dev:
    - {host: "h1.example.com", name: "h1", tags:["us-east1", "vpc-1234567"]}
    - {host: "h2.example.com", port: 2233, name: "h2"}
    - {host: "h3.example.com", user: "user1"}
    - {host: "h4.example.com", user: "user2", name: "h4"}
  staging:
    - {host: "h5.example.com", port: 2233, name: "h5"}
    - {host: "h6.example.com", user: "user3", name: "h6"}
```

- host: the host name or IP address of the remote host.
- port: the ssh port of the remote host. Optional, default is 22.
- user: the ssh user of the remote host. Optional, default is the user defined in the playbook file or `--user` flag.
- name: the name of the remote host. Optional.
- tags: the list of tags of the remote host. Optional.

In case if port not defined, the default port 22 will be used. If user not defined, the playbook's user will be used. 

This is an example of the inventory file with hosts only (no groups)

```yaml
hosts:
  - {host: "hh1.example.com", name: "hh1"}
  - {host: "hh2.example.com", port: 2233, name: "hh2", user: "user1"}
  - {host: "h2.example.com", port: 2233, name: "h2", tags:["us-east1", "vpc-1234567"]}
  - {host: "h3.example.com", user: "user1", name: "h3"}
  - {host: "h4.example.com", user: "user2", name: "h4"}
```
This format is useful when you want to define a list of hosts without groups.

In each case inventory automatically merged and a special group `all` will be created that contains all the hosts.

*Alternatively, the inventory can be represented using the TOML format.*

## Runtime variables

Spot supports runtime variables that can be used in the playbook file. The following variables are supported:

- `{SPOT_REMOTE_HOST}`: The remote host name or IP address.
- `{SPOT_REMOTE_NAME}`: The remote custom name, set in inventory or playbook as `name`.
- `{SPOT_REMOTE_USER}`: The remote username.
- `{SPOT_COMMAND}`: The command name.
- `{SPOT_TASK}`: The task name.
- `{SPOT_ERROR}`: The error message, if any.

Variables can be used in the following places: `script`, `copy`, `sync`, `delete`, `wait` and `env`, for example:

```yaml
tasks:
  deploy-things:
    commands:
      - name: copy configuration
        copy: {"src": "{SPOT_REMOTE_HOST}/conf.yml", "dst": "/tmp/conf.yml", "mkdir": true}
      - name: sync things
        sync: {"src": "testdata", "dst": "/tmp/{SPOT_TASK}/things"}
      - name: some command
        script: |
          ls -laR /tmp/${SPOT_COMMAND}
        env: { FOO: bar, BAR: "{SPOT_COMMAND}-blah" }
      - name: delete things
        delete: {"loc": "/tmp/things/{SPOT_REMOTE_USER}", "recur": true}

```

## Ad-hoc commands

Spot supports ad-hoc commands that can be executed on the remote hosts. This is useful when all is needed is to execute a command on the remote hosts without creating a playbook file. This command optionally passed as a first argument, i.e. `spot "la -la /tmp"` and usually accompanied by the `--target=<host>` (`-t <host>`) flags. Example: `spot "ls -la" -t h1.example.com -t h2.example.com`. 

All other overrides can be used with adhoc commands as well, for example `--user`and `--key` to specify the user and sshkey to use when connecting to the remote hosts. By default, Spot will use the current user and the default ssh key. Inventory can be passed to such commands as well, for example `--inventory=inventory.yml`.

Adhoc commands always sets `verbose` to `true` automatically, so the user can see the output of the command.


## Rolling Updates

Spot supports rolling updates, which means that the tasks will be executed on the hosts one by one, waiting for the previous host to finish before starting the next one. This is useful when you need to update a service running on multiple hosts, but want to avoid downtime. To enable rolling updates, use the `--concurrent=N` flag when running the `spot` command. `N` is the number of hosts to execute the tasks on concurrently. Example: `spot --concurrent=2`. In addition, user can use a builtin `wait` command to wait for a service to start before executing the next command. See the [Command Types](#command-types) section for more details. Practically, user will have a task with a series of commands, where the last command will wait for the service to start by running a command like `curl -s --fail localhost:8080` and then the task will be executed on the next host.


## Why Spot?

Spot is designed to provide a simple, efficient, and flexible solution for deployment and configuration management. 
It addresses the need for a tool that is easy to set up and use, while still offering powerful features for managing infrastructure.
Below are some of the reasons why you should consider using Spot:

1. **Simplicity**: Spot's primary goal is to be as simple as possible without sacrificing functionality. Its configuration is written in YAML, making it easy to read and understand. You can quickly create and manage tasks, targets, and commands without dealing with complex structures or concepts.
2. **Flexibility**: Spot is designed to be flexible and adaptable to various deployment and configuration scenarios. You can use it to manage different targets, such as production, staging, and development environments. It supports executing tasks on remote hosts directly or through inventory files and URLs, allowing you to use existing inventory management solutions.
3. **Extensibility**: Spot is built to be extensible, allowing you to define custom scripts for execution on remote hosts, as well as offering built-in commands for common operations such as copy, sync, and delete. This extensibility enables you to create complex workflows for deployment and configuration management, tailored to your specific needs.
4. **Concurrent Execution**: Spot supports concurrent execution of tasks, allowing you to speed up the deployment and configuration processes by running multiple tasks simultaneously. This can be particularly helpful when managing large-scale infrastructure or when time is of the essence.
5. **Customizable**: Spot provides various command-line options and environment variables that enable you to customize its behavior according to your requirements. You can easily modify the playbook file, task, target, and other parameters, as well as control the execution flow by skipping or running specific commands.
6. **Lightweight**: Spot is a lightweight tool, written in Go, that does not require heavy dependencies or a complex setup process. It can be easily installed and run on various platforms, making it an ideal choice for teams looking for a low-overhead solution for deployment and configuration management.

In conclusion, Spot is a powerful and easy-to-use tool that simplifies the process of deployment and configuration management while offering the flexibility and extensibility needed to cater to various use cases. If you value simplicity, efficiency, and a customizable experience, Spot is a great choice for your infrastructure management needs.


### Is it replacing Ansible?

Spot is not intended to be a direct replacement for Ansible. While both tools can be used for deployment and configuration management, there are some key differences between them:

- **Complexity**: Ansible is a more feature-rich and mature tool, offering a wide range of modules and plugins that can automate many different aspects of infrastructure management. Spot, on the other hand, is designed to be simple and lightweight, focusing on a few core features to streamline the deployment and configuration process.
- **Learning Curve**: Due to its simplicity, Spot has a lower learning curve compared to Ansible. It's easier to get started with Spot, making it more suitable for smaller projects or teams with limited experience in infrastructure automation. Ansible, while more powerful, can be more complex to learn and configure, especially for newcomers. 
- **Customization**: While both tools offer customization options, Ansible has a more extensive set of built-in modules and plugins that can handle a wide range of tasks out-of-the-box. Spot, in contrast, relies on custom scripts and a limited set of built-in commands for its functionality, which might require more manual configuration and scripting for certain use cases.
- **Community and Ecosystem**: Ansible has a large and active community, as well as a vast ecosystem of roles, modules, and integrations. This can be beneficial when dealing with common tasks or integrating with third-party systems. Spot, being a smaller and simpler tool, doesn't have the same level of community support or ecosystem.
- **Ease of installation and external dependencies**: One of the most significant benefits of Spot is that it has no dependencies. Being written in Go, it is compiled into a single binary that can be easily distributed and executed on various platforms. This eliminates the need to install or manage any additional software, libraries, or dependencies to use Spot. Ansible, on the other hand, is written in Python and requires Python to be installed on both the control host (where Ansible is run) and the managed nodes (remote hosts being managed). Additionally, Ansible depends on several Python libraries, which need to be installed and maintained on the control host. Some Ansible modules may also require specific libraries or packages to be installed on the managed nodes, adding to the complexity of managing dependencies.

Spot can be a good choice if you're looking for a lightweight, simple, and easy-to-use tool for deployment and configuration management, particularly for smaller projects or when you don't need the extensive features offered by Ansible. However, if you require a more comprehensive solution with a wide range of built-in modules, plugins, and integrations, Ansible might be a better fit for your needs. 

The simplicity of Spot's single binary distribution and lack of dependencies make it an attractive choice for teams who want a lightweight, easy-to-install, and low-maintenance solution for deployment and configuration management. While Ansible offers more advanced features and a comprehensive ecosystem, its dependency on Python and additional libraries can be a hurdle for some users, particularly in environments with strict control over software installations or limited resources.


## Status

The project is currently in active development, and breaking changes may occur until the release of version 1.0. However, we strive to minimize disruptions and will only introduce breaking changes when there is a compelling reason to do so.

## Contributing

Please feel free to open a discussion, submit issues, fork the repository, and send pull requests. See [CONTRIBUTING.md](https://github.com/umputun/spot/blob/master/CONTRIBUTING.md) for more information.

## License

This project is licensed under the MIT License. See the LICENSE file for more information.
