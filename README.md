# Pod volume backup list

Kubectl Krew plugin to list pod volume backups taken by Velero.

Takes one required argument, name of Velero backup, and a second optional argument to set which namespace Velero is running in (default `velero`).

Pod volume backups are listed in alphabetic order.

## Usage

Check in Velero which backups you have:

```shell
velero get backups
```

Choose the backup you want to list pod volume backups from, like `velero-nightly-20251014050055`, and use that as an argument for the plugin:

```shell
kubectl podvolumebackup-list velero-nightly-20251015050033
```

And now you should get a list of your pod volume backups.

## Requirements

`jq` and `numfmt`

## Installation

See release notes.
