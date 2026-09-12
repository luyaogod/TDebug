# doc: 11_user-interface/2152-syntax-of-the-declarative-dialog-block.md —— 声明式 DIALOG name(params)：带类型参数 + 单个 record-input-block + END DIALOG
DIALOG cust_input(cust_num INTEGER)
    INPUT BY NAME cust_num
        BEFORE INPUT
            DISPLAY "x"
    END INPUT
END DIALOG
