package store

// Indexed generated columns keep one canonical representation per entity while
// SQLite enforces ownership, uniqueness and referential integrity. Deferred
// foreign keys allow an entire user/device revocation in a single transaction.
var schema = []string{
	`CREATE TABLE metadata(id INTEGER PRIMARY KEY CHECK(id=1), revision INTEGER NOT NULL, observed_revision INTEGER NOT NULL, last_error TEXT NOT NULL)`,
	`INSERT INTO metadata VALUES(1,0,0,'')`,
	`CREATE TABLE users(id TEXT PRIMARY KEY NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)), alias TEXT GENERATED ALWAYS AS(nullif(lower(json_extract(data,'$.alias')),'')) VIRTUAL UNIQUE)`,
	`CREATE TABLE resources(id TEXT PRIMARY KEY NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)))`,
	`CREATE TABLE devices(id TEXT PRIMARY KEY NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)), user_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.user_id')) VIRTUAL NOT NULL REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED)`,
	`CREATE INDEX devices_user ON devices(user_id)`,
	`CREATE TABLE vpn_leases(id TEXT PRIMARY KEY NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)), user_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.user_id')) VIRTUAL NOT NULL REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED, device_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.device_id')) VIRTUAL NOT NULL REFERENCES devices(id) DEFERRABLE INITIALLY DEFERRED)`,
	`CREATE INDEX vpn_leases_device ON vpn_leases(device_id)`,
	`CREATE TABLE credentials(id TEXT PRIMARY KEY NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)), user_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.user_id')) VIRTUAL NOT NULL REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED, device_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.device_id')) VIRTUAL NOT NULL REFERENCES devices(id) DEFERRABLE INITIALLY DEFERRED)`,
	`CREATE INDEX credentials_device ON credentials(device_id)`,
	`CREATE TABLE operation_keys(id TEXT PRIMARY KEY NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)))`,
	`CREATE TABLE account_cleanups(id TEXT PRIMARY KEY NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)))`,
	`CREATE INDEX account_cleanup_due ON account_cleanups(julianday(json_extract(data,'$.cleanup_after')),id)`,
	`CREATE TABLE hosts(id TEXT PRIMARY KEY NOT NULL REFERENCES devices(id) DEFERRABLE INITIALLY DEFERRED, data TEXT NOT NULL CHECK(json_valid(data)), revision INTEGER GENERATED ALWAYS AS(json_extract(data,'$.revision')) VIRTUAL NOT NULL CHECK(revision>0))`,
	`CREATE TABLE remote_grants(id TEXT PRIMARY KEY NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)), user_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.user_id')) VIRTUAL NOT NULL REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED, host_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.host_id')) VIRTUAL NOT NULL REFERENCES hosts(id) DEFERRABLE INITIALLY DEFERRED, UNIQUE(user_id,host_id))`,
	`CREATE TABLE remote_sessions(id TEXT PRIMARY KEY NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)), user_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.user_id')) VIRTUAL NOT NULL REFERENCES users(id) DEFERRABLE INITIALLY DEFERRED, host_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.host_id')) VIRTUAL NOT NULL REFERENCES hosts(id) DEFERRABLE INITIALLY DEFERRED, controller_id TEXT GENERATED ALWAYS AS(json_extract(data,'$.controller_id')) VIRTUAL NOT NULL REFERENCES devices(id) DEFERRABLE INITIALLY DEFERRED, state TEXT GENERATED ALWAYS AS(json_extract(data,'$.state')) VIRTUAL NOT NULL CHECK(state IN ('issued','active','closed')))`,
	`CREATE UNIQUE INDEX remote_host_active ON remote_sessions(host_id) WHERE state IN ('issued','active')`,
	`CREATE INDEX remote_controller ON remote_sessions(controller_id,state)`,
	`CREATE INDEX remote_history_time ON remote_sessions(julianday(json_extract(data,'$.created_at')),id) WHERE state='closed'`,
	`CREATE TABLE remote_operations(id TEXT PRIMARY KEY NOT NULL, data TEXT NOT NULL CHECK(json_valid(data)))`,
	`CREATE TABLE ticket_nonces(id TEXT PRIMARY KEY NOT NULL CHECK(length(id)=64), expires_ms INTEGER NOT NULL)`,
	`CREATE INDEX nonce_expiry ON ticket_nonces(expires_ms)`,
	`CREATE TABLE audit_events(sequence INTEGER PRIMARY KEY AUTOINCREMENT, data TEXT NOT NULL CHECK(json_valid(data)))`,
	`PRAGMA application_id=1313296178`,
	`PRAGMA user_version=4`,
}
