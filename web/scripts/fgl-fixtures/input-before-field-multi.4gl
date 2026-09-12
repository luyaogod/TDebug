# doc: 11_user-interface/1942-before-field-block.md —— BEFORE FIELD / ON CHANGE / AFTER FIELD 的逗号多字段列表
FUNCTION inp_fields()
    INPUT BY NAME g_cust
        BEFORE FIELD cust_name, cust_addr, cust_zip
            DISPLAY "f"
        ON CHANGE cust_name, cust_addr
            DISPLAY "c"
        AFTER FIELD cust_zip
            DISPLAY "a"
    END INPUT
END FUNCTION
