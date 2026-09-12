# doc: 10_sql-support/1155-foreach-result-set-cursor.md —— FOREACH cid [INTO fvar] … END FOREACH
FUNCTION f_foreach()
    DEFINE l_code CHAR(20)
    DECLARE c_list CURSOR FOR SELECT code FROM t1
    FOREACH c_list INTO l_code
        DISPLAY l_code
    END FOREACH
END FUNCTION
