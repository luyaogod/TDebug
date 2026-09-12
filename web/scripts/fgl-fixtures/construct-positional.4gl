# doc: 11_user-interface/2048-syntax-of-construct-instruction.md —— CONSTRUCT variable ON column-list FROM field-list 位置绑定形式
FUNCTION qbe_pos()
    CONSTRUCT q_cust ON cust_name, cust_addr FROM f1, f2
        BEFORE FIELD f1
            DISPLAY "b"
    END CONSTRUCT
END FUNCTION
