# doc: 10_sql-support/1158-declare-select-for-update.md —— DECLARE cid CURSOR FOR { select-statement }，select 里带 FOR UPDATE 锁定子句
# 第 7 行的 FOR UPDATE 被折到行首。它必须不被当成 FOR 块，否则第 10 行的 END FOR 会配到它身上。
FUNCTION f_locked()
    DEFINE l_i INTEGER
    FOR l_i = 1 TO 2
        DECLARE c_upd CURSOR FOR
            SELECT code FROM t1
            FOR UPDATE
        OPEN c_upd
        CLOSE c_upd
    END FOR
END FUNCTION
