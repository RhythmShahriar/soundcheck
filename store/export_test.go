// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package store

// VerifyShots lets the external tests check a snapshot the way Promote
// does.
var VerifyShots = verifyShots

// LockWait lets the external tests shorten the wait for a held lock.
var LockWait = &lockWait

// LockFile is the store's lock file name.
const LockFile = lockFile
