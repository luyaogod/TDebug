# doc: 11_user-interface/2078-syntax-of-the-procedural-dialog-instruction.md —— DIALOG ATTRIBUTES(...) + dialog-control-block（BEFORE/AFTER DIALOG、ON IDLE、ON TIMER、ON KEY）
FUNCTION ui_main()
    DIALOG ATTRIBUTES(UNBUFFERED)
        INPUT BY NAME g_cust
            ON ACTION accept
                EXIT DIALOG
        END INPUT
        BEFORE DIALOG
            DISPLAY "start"
        ON IDLE 10
            DISPLAY "idle"
        ON TIMER 5
            DISPLAY "tick"
        ON KEY (F11)
            DISPLAY "help"
        AFTER DIALOG
            DISPLAY "end"
    END DIALOG
END FUNCTION
