# doc: 11_user-interface/2152-syntax-of-the-declarative-dialog-block.md —— 声明式 DIALOG 的 record-name record-type INOUT 参数形式
TYPE t_comment RECORD
    c_text VARCHAR(200)
END RECORD

DIALOG comment_input(rc t_comment INOUT)
    INPUT BY NAME rc
    END INPUT
END DIALOG
