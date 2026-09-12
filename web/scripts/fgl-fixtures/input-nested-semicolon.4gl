# doc: 08_language-basics/0549-statement-terminator.md —— ; 收尾的嵌套 dialog：ON ACTION 里的 INPUT 用 ; 结束，外层 END INPUT 归外层
FUNCTION inp_nest()
    INPUT BY NAME g_cust1
        ON ACTION other_input
            INPUT BY NAME g_cust2 ;
    END INPUT
END FUNCTION
