# doc: 11_user-interface/2007-syntax-of-input-array-instruction.md —— INPUT ARRAY array FROM screen-array.* + BEFORE ROW / ON ROW CHANGE
FUNCTION ia_edit()
    DEFINE arr DYNAMIC ARRAY OF RECORD
        id INTEGER
    END RECORD
    INPUT ARRAY arr FROM sa.*
        BEFORE ROW
            DISPLAY "r"
        ON ROW CHANGE
            DISPLAY "c"
    END INPUT
END FUNCTION
