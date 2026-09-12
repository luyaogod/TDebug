# doc: 11_user-interface/2048-syntax-of-construct-instruction.md —— CONSTRUCT BY NAME variable ON column-list 形式
FUNCTION qbe_name()
    CONSTRUCT BY NAME q_cust ON cust_name, cust_addr
        BEFORE CONSTRUCT
            DISPLAY "b"
        AFTER FIELD cust_name
            DISPLAY "a"
    END CONSTRUCT
END FUNCTION
