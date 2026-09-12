# doc: 08_language-basics/0764-scope-of-a-function.md —— PUBLIC / PRIVATE 前缀，且 RETURNS () 允许
PUBLIC FUNCTION pub_f() RETURNS ()
    RETURN
END FUNCTION

PRIVATE FUNCTION priv_f()
    CALL pub_f()
END FUNCTION
