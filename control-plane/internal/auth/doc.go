// Package auth will hold JWT issuance/verification, Argon2id password hashing,
// and AES-256-GCM credential encryption (key derived from the user's login
// password; never stored). Implemented in step 5.
package auth
