# doc: 11_user-interface/1906-syntax-of-the-menu-instruction.md / 1914-before-menu-block.md —— BEFORE MENU 控制块；COMMAND 在 MENU 里是 menu-option（叶子，不建节点）
FUNCTION m_opts()
    MENU "Main"
        BEFORE MENU
            DISPLAY "b"
        COMMAND "Quit" "Leave" HELP 1
            EXIT MENU
        ON ACTION refresh
            DISPLAY "r"
        ON IDLE 3
            DISPLAY "i"
        ON TIMER 2
            DISPLAY "t"
    END MENU
END FUNCTION
