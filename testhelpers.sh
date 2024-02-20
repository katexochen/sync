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
    curl -fsSL "localhost:8080/mutex/$UUID/unlock/$NONCE"
}

function deleteMutex() {
    curl -fsSL "localhost:8080/mutex/$UUID"
}

function newFifo() {
    UUID=$(curl -fsS localhost:8080/fifo/new | jq -r '.uuid')
    export UUID
}

function ticketFifo() {
    TICKET=$(curl -fsS "localhost:8080/fifo/$UUID/ticket" | jq -r '.ticket')
    export TICKET
}

function waitFifo() {
    curl -fsSL "localhost:8080/fifo/$UUID/wait/$TICKET"
}

function doneFifo() {
    curl -fsSL "localhost:8080/fifo/$UUID/done/$TICKET"
}
