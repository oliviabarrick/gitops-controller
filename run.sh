#!/bin/sh

if [ ! -z "$SSH_KEY_PATH" ]; then
    eval $(ssh-agent)
    ssh-add $SSH_KEY_PATH
fi

exec /gitops-controller "$@"
