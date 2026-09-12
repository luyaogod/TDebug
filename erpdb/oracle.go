package erpdb

// OracleConnector:Oracle 在线查询连接(go-ora 驱动,纯 Go)。
// 与 KingbaseConnector 对齐 Connector 接口:Type/ServerVersion/Query/Close。
// 查询统一走只读单语句校验(见 checkReadOnlySQL),列值一律转字符串返回。

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"tdebug/dbconfig"

	_ "github.com/sijms/go-ora/v2" // 注册 sql 驱动 "oracle"
)

// OracleConnector 用 go-ora(database/sql)连 Oracle,查询文本化返回。
type OracleConnector struct {
	addr string
	typ  string
	db   *sql.DB
}

// OpenOracle 连接 Oracle。统一显式模型:host/port/service(service 也接受 database 旧值);
// service 为空时用 host 的 database 字段兜底。
func OpenOracle(ctx context.Context, c dbconfig.Connection) (*OracleConnector, error) {
	server, port, service := c.Host, c.Port, c.Svc()
	if port == 0 {
		port = 1521
	}
	if server == "" || service == "" {
		return nil, fmt.Errorf("Oracle 连接缺少地址或服务名 (需填 host/port/service)")
	}
	dsn := goOraDSN(server, port, service, c.User, c.Password)
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 Oracle 连接配置失败: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接 Oracle 失败 (%s:%d/%s): %w", server, port, service, err)
	}
	return &OracleConnector{addr: c.Address(), typ: c.Type, db: db}, nil
}

func goOraDSN(host string, port int, service, user, password string) string {
	return fmt.Sprintf("oracle://%s:%s@%s/%s", urlEscape(user), urlEscape(password),
		net.JoinHostPort(host, strconv.Itoa(port)), urlEscape(service))
}

func urlEscape(s string) string {
	r := strings.NewReplacer("%", "%25", ":", "%3A", "@", "%40", "/", "%2F", "?", "%3F")
	return r.Replace(s)
}

func (o *OracleConnector) Type() string { return o.typ }

func (o *OracleConnector) ServerVersion(ctx context.Context) (string, error) {
	_, rows, err := o.Query(ctx, "SELECT banner FROM v$version WHERE ROWNUM = 1")
	if err != nil {
		return "", err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return "", fmt.Errorf("v$version 未返回结果")
	}
	return rows[0][0], nil
}

// Query 只读单语句查询;列值统一转字符串(与 Kingbase 文本协议语义一致)。
func (o *OracleConnector) Query(ctx context.Context, sql string) ([]string, [][]string, error) {
	if err := checkReadOnlySQL(sql); err != nil {
		return nil, nil, err
	}
	rows, err := o.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	var result [][]string
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for rows.Next() {
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			row[i] = scanString(v)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return cols, result, nil
}

// scanString 把 database/sql 扫到的任意值文本化(供 Oracle 走 interface 扫描)。
func scanString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	case time.Time:
		return t.Format("2006-01-02 15:04:05")
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func (o *OracleConnector) Close() {
	if o.db != nil {
		o.db.Close()
	}
}
