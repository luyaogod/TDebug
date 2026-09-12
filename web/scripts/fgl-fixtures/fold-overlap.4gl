# doc: 11_user-interface/2078-syntax-of-the-procedural-dialog-instruction.md + 08_language-basics/0682-if.md
# 块树折叠区（FUNCTION、DIALOG）与 FOLD_ONLY 折叠区（IF）必须共存，且不重复。
FUNCTION f_overlap()
    IF l_ok THEN
        DIALOG
            DISPLAY "d"
        END DIALOG
    END IF
END FUNCTION
