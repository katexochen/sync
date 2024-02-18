#!/usr/bin/env bash

function newMutex() {
    UUID=$(curl -fsS localhost:8080/mutex/new | jq -r '.uuid')
    export UUID
}

function lockMutex() {
    NONCE=$(curl -fsS "localhost:8080/mutex/$UUID/lock" | jq -r '.nonce')
    export NONCE
}

function unlockMutex() {
    curl -fsS "localhost:8080/mutex/$UUID/unlock/$NONCE"
}

function deleteMutex() {
    curl -fsS "localhost:8080/mutex/$UUID"
}
