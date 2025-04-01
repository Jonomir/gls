# gls

This is a small go program that syncs gitlab projects to your machine, preserving the group structure

## Building

build with

```sh
go build -o gls cmd/main.go
```


## Configuration

Configuration can be done in three ways. These are the priorities

1. Flags, see --help for more info
2. Environment variables 
3. Config file `~/.gls`


Environment variables have the prefix `GLS_`

Config must me located in the users home dir at `~/.gls`

Example:
```
WORKERS=10
GITLAB_TOKEN=<token>
PATH_GITLAB=<companyname>
PATH_LOCAL=/Users/<username>/Projects
```

## Dependencies

This tool needs git to be present in the PATH