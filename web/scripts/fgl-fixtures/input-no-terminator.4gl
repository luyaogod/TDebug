# doc: 11_user-interface/1932-syntax-of-the-input-instruction.md —— END INPUT 在语法上可选：无 dialog-control-block 时 INPUT 只是一条语句
FUNCTION inp_bare()
    INPUT BY NAME g_cust
    DISPLAY "done"
END FUNCTION
