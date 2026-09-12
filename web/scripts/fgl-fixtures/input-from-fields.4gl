# doc: 11_user-interface/1932-syntax-of-the-input-instruction.md —— INPUT {variable|record.*} FROM field-list 的位置绑定形式
FUNCTION inp_pos()
    INPUT r_cust FROM cust_name, cust_addr
        BEFORE INPUT
            DISPLAY "x"
    END INPUT
END FUNCTION
