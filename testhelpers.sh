#!/usr/bin/env bash

export URL="http://localhost:8080"

function newMutex() {
    UUID=$(curl -fsS $URL/mutex/new | jq -r '.uuid')
    export UUID
}

function lockMutex() {
    NONCE=$(curl -fsS "$URL/mutex/$UUID/lock" | jq -r '.nonce')
    export NONCE
}

function unlockMutex() {
    curl -fsSL "$URL/mutex/$UUID/unlock/$NONCE"
}

function deleteMutex() {
    curl -fsSL "$URL/mutex/$UUID"
}

function newFifo() {
    UUID=$(curl -fsS $URL/fifo/new | jq -r '.uuid')
    export UUID
}

function ticketFifo() {
    TICKET=$(curl -fsS "$URL/fifo/$UUID/ticket" | jq -r '.ticket')
    export TICKET
}

function waitFifo() {
    curl -fsSL "$URL/fifo/$UUID/wait/$TICKET"
}

function doneFifo() {
    curl -fsSL "$URL/fifo/$UUID/done/$TICKET"
}
