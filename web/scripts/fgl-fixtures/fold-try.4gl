# doc: 09_advanced-features/0853-try-catch-block.md —— TRY … CATCH … END TRY
FUNCTION f_try()
    DEFINE l_n INTEGER
    TRY
        SELECT COUNT(*) INTO l_n FROM t1
    CATCH
        DISPLAY "err"
    END TRY
END FUNCTION
