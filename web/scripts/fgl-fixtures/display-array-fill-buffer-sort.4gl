# doc: 11_user-interface/1968-on-fill-buffer-block.md / 1988 / 1989 / 1984-1987 —— DISPLAY ARRAY 的 ON FILL BUFFER / ON SELECTION CHANGE / ON SORT / ON APPEND|INSERT|UPDATE|DELETE
FUNCTION da_a()
    DISPLAY ARRAY arr TO sa.*
        ON FILL BUFFER
            DISPLAY "1"
        ON SELECTION CHANGE
            DISPLAY "2"
        ON SORT
            DISPLAY "3"
        ON APPEND
            DISPLAY "4"
        ON INSERT
            DISPLAY "5"
        ON UPDATE
            DISPLAY "6"
        ON DELETE
            DISPLAY "7"
    END DISPLAY
END FUNCTION
