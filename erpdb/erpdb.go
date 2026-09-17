package erpdb

import (
	"context"
	"fmt"
	"time"

	"tdebug/dbconfig"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"tdebug/safesql"
)

// Connector is a live connection to an ERP database (Kingbase/Oracle).
type Connector interface {
	// Type returns the connection type ("kingbase" | "oracle").
	Type() string
	// ServerVersion runs SELECT version() and returns the server banner.
	ServerVersion(ctx context.Context) (string, error)
	// Query executes a read-only SQL statement and returns columns and rows as strings.
	Query(ctx context.Context, sql string) ([]string, [][]string, error)
	// Close releases the underlying connection pool.
	Close()
}

// Open creates a connector for the given connection config by type.
func Open(ctx context.Context, c dbconfig.Connection) (Connector, error) {
	// 客户端直连凭据 = 账号列表首项(直连与 TOPENT 无关)
	if err := c.FillDialCred(); err != nil {
		return nil, err
	}
	switch c.Type {
	case "kingbase":
		return OpenKingbase(ctx, c)
	case "oracle":
		return OpenOracle(ctx, c)
	default:
		return nil, fmt.Errorf("未知的连接类型 \"%s\"", c.Type)
	}
}

// KingbaseConnector connects to a Kingbase (人大金仓) server via the PostgreSQL wire protocol.
type KingbaseConnector struct {
	addr string
	typ  string
	pool *pgxpool.Pool
}

// OpenKingbase connects to the given Kingbase server and verifies the connection.
func OpenKingbase(ctx context.Context, c dbconfig.Connection) (Connector, error) {
	poolCfg, err := pgxpool.ParseConfig(kingbaseDSN(c))
	if err != nil {
		return nil, fmt.Errorf("解析 Kingbase 连接配置失败: %w", err)
	}
	// Use the simple query protocol: results come back in text format, so every
	// column can be scanned into a string (binary format can't decode into
	// **string), and it also avoids Kingbase's extended-protocol quirks.
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("创建 Kingbase 连接池失败: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接 Kingbase 失败 (%s): %w", c.Address(), err)
	}

	return &KingbaseConnector{addr: c.Address(), typ: c.Type, pool: pool}, nil
}

func kingbaseDSN(c dbconfig.Connection) string {
	return fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=disable connect_timeout=10",
		c.Host, c.Port, c.Database, c.User, c.Password)
}

func (k *KingbaseConnector) Type() string { return k.typ }

func (k *KingbaseConnector) ServerVersion(ctx context.Context) (string, error) {
	_, rows, err := k.Query(ctx, "SELECT version()")
	if err != nil {
		return "", err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return "", fmt.Errorf("SELECT version() 未返回结果")
	}
	return rows[0][0], nil
}

// Query executes a read-only SQL statement and returns columns and rows as strings.
// NULL values are returned as empty strings.
func (k *KingbaseConnector) Query(ctx context.Context, sql string) ([]string, [][]string, error) {
	if err := checkReadOnlySQL(sql); err != nil {
		return nil, nil, err
	}

	rows, err := k.pool.Query(ctx, sql)
	if err != nil {
		return nil, nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	fields := rows.FieldDescriptions()
	columns := make([]string, len(fields))
	for i, f := range fields {
		columns[i] = f.Name
	}

	// Simple protocol returns results in text format, so RawValues() gives the
	// raw text bytes per value — no decode/alloc per value. This is an order of
	// magnitude faster than scanning into **string.
	var result [][]string
	for rows.Next() {
		raw := rows.RawValues()
		row := make([]string, len(raw))
		for i, v := range raw {
			if v != nil {
				row[i] = string(v)
			}
		}
		result = append(result, row)
	}
	if rows.Err() != nil {
		return nil, nil, rows.Err()
	}
	return columns, result, nil
}

func (k *KingbaseConnector) Close() {
	if k.pool != nil {
		k.pool.Close()
	}
}

// checkReadOnlySQL 只读校验 —— 统一到 safesql。
//
// 这里原来是一份**独立**的黑名单(前缀匹配 + 禁分号),而它是可绕过的:
// `WITH x AS (…) DELETE`、`/*x*/DELETE`、`CALL`、注释穿插都能过。
// 同一个工具里并存两套不一致的"只读"语义本身就是隐患 —— 何况弱的那套还在活路径上
// (hConnTest 走的就是这里)。现在两边同一份实现、同一套测试。
func checkReadOnlySQL(sql string) error {
	return safesql.Check(sql)
}
