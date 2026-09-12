# doc: 12_reports/2474-the-report-routine.md —— REPORT 的 Syntax 1（旧式参数表）与 Syntax 2（类型化参数表）+ PUBLIC/PRIVATE
PRIVATE REPORT rep_legacy(cust_num)
    DEFINE cust_num INTEGER
    FORMAT EVERY ROW
        PRINT cust_num
END REPORT

PUBLIC REPORT rep_typed(cust_num INTEGER)
    FORMAT EVERY ROW
        PRINT cust_num
END REPORT
