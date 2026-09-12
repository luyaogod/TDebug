# doc: 08_language-basics/0718-attributes-on-record-definitions.md 示例 —— TYPE t_cust RECORD ATTRIBUTES(json_name="customer")
TYPE t_cust RECORD ATTRIBUTES(json_name="customer")
    cust_id INTEGER,
    cust_name VARCHAR(50)
END RECORD
