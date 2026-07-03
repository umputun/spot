# Import Tasks Example

This example demonstrates how to organize a deployment playbook using the task
import feature. Tasks are split into separate files for each phase.

## Structure

```
import-tasks/
├── deploy.yml          # main playbook with imports
├── tasks/
│   ├── prepare.yml     # preparation tasks
│   ├── deploy-app.yml  # application deployment tasks
│   └── verify.yml      # verification tasks
```

## Usage

Run the playbook against the `prod` target:

```bash
spot -p .examples/import-tasks/deploy.yml -t prod
```

Dry run to preview without executing:

```bash
spot -p .examples/import-tasks/deploy.yml -t prod --dry
```
