# doc: 11_user-interface/2078-syntax-of-the-procedural-dialog-instruction.md —— dialog-control-block 的 COMMAND / COMMAND KEY 形式
FUNCTION cmd_dlg()
    DIALOG
        INPUT BY NAME g_cust
        END INPUT
        COMMAND "Apply" "Applies" HELP 100
        COMMAND KEY (F2) "Print"
    END DIALOG
END FUNCTION
