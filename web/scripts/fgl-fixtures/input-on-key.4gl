# doc: 11_user-interface/1948-on-key-block.md —— ON KEY ( key-name [,...] ) 键名列表形式
FUNCTION inp_key()
    INPUT BY NAME g_cust
        ON KEY (F1, Control-z)
            DISPLAY "k"
    END INPUT
END FUNCTION
