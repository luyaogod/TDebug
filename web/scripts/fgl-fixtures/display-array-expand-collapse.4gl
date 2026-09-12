# doc: 11_user-interface/1969-on-expand-block.md / 1970-on-collapse-block.md —— ON EXPAND ( row-index ) / ON COLLAPSE ( row-index ) —— 带括号参数的事件
FUNCTION da_b()
    DEFINE row_idx INTEGER
    DISPLAY ARRAY arr TO sa.*
        ON EXPAND (row_idx)
            DISPLAY "1"
        ON COLLAPSE (row_idx)
            DISPLAY "2"
    END DISPLAY
END FUNCTION
