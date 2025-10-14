# Pod volume backup list

Kubectl Krew plugin to list pod volume backups taken by Velero.

Takes one required argument, name of Velero backup, and a second optional argument to set which namespace Velero is running in (default `velero`).

Pod volume backups are listed in alphabetic order.

## Requirements

`jq` and `numfmt`
