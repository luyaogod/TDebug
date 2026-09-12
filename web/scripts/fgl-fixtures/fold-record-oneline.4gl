# doc: 08_language-basics/0717-record.md Syntax 2 —— RECORD LIKE tabname.* 是一行式定义，没有 END RECORD
# 第 4 行的 RECORD 不在行尾（后面跟 LIKE …），因此不能开启 RECORD 块；否则第 6 行的 END RECORD 会配到它身上。
FUNCTION f_oneline()
    DEFINE l_outer RECORD
        l_inner RECORD LIKE pmdl_t.*
        code CHAR(10)
    END RECORD
    DISPLAY l_outer.code
END FUNCTION
