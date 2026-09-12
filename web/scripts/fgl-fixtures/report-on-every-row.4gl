# doc: 12_reports/2485-on-every-row.md —— FORMAT 段的 ON EVERY ROW 控制块（语料 0 例）
REPORT rep_oer(cust_num)
    DEFINE cust_num INTEGER
    FORMAT
        ON EVERY ROW
            PRINT cust_num
END REPORT
