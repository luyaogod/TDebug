# doc: 08_language-basics/0717-record.md —— RECORD … END RECORD；DEFINE x DYNAMIC ARRAY OF RECORD 同样以 RECORD 收尾
TYPE t_cust RECORD
    cust_id INTEGER,
    cust_name VARCHAR(50)
END RECORD

FUNCTION f_record()
    DEFINE l_arr DYNAMIC ARRAY OF RECORD
        code CHAR(10)
    END RECORD
    DISPLAY l_arr[1].code
END FUNCTION
