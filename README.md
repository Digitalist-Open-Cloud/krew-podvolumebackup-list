# Pod volume backup list

Kubectl Krew plugin to list pod volume backups taken by Velero.

Pod volume backups (PodVolumeBackup) is a custom resource created while doing volume backups with Velero. This plugin reads some data from the custom resource and displays it in a more human readable way.

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

If you want to list pod volume backups from all backups, instead of providing the backup name, provide the `--all` flag:

```shell
kubectl podvolumebackup-list --all
```

Optionally, list only pod volume backups matching pod name, like:

```shell
kubectl podvolumebackup-list --all --pod=nginx
```

Or:

```shell
kubectl podvolumebackup-list velero-nightly-20251015050033 --pod=nginx
```

## Requirements

`jq` and `numfmt`

## Installation

See release notes.
