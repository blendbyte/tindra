package storage

import "github.com/jackc/pgx/v5"

// userQueryArgs keeps value-sensitive user filters out of PostgreSQL's generic
// prepared-plan cache. A user may match five rows or hundreds of thousands;
// similarly, short and long picker searches need different access paths.
// Planning with the actual values lets ordered scans stop at LIMIT for hot
// users while rare users use selective lookups. pgx quotes and escapes every
// parameter; callers must continue to use placeholders, not interpolate values.
// Other queries retain the default prepared-statement protocol.
func userQueryArgs(value string, args []any) []any {
	if value == "" {
		return args
	}
	return append([]any{pgx.QueryExecModeSimpleProtocol}, args...)
}
