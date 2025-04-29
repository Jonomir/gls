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

Config must be located in the users home dir at `~/.gls`

Minimum viable Config:
```
GITLAB_TOKEN=<token>
GITLAB_GROUP=<companyname>
LOCAL_PATH=~/Projects
```

Full Config:
```
WORKERS=10

GITLAB_URL=https://gitlab.example.com
GITLAB_TOKEN=<token>
GITLAB_GROUP=<companyname>

LOCAL_PATH=~/Projects
LOCAL_SSH_KEY=~/.ssh/my_key
LOCAL_SSH_PASSPHRASE=<secret>
```
