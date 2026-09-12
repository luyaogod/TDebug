# doc: 08_language-basics/0550-source-comments.md —— { } 块注释里的 INPUT/END INPUT 不得产生幻影节点（基线）
FUNCTION c_brace()
    {
    INPUT BY NAME g_cust
    END INPUT
    }
    DISPLAY "ok"
END FUNCTION
