"""Create a customer.

The external id is the customer's natural key: creating it twice answers 409 instead of making a second customer.
"""

from payrudder import ConflictError, Payrudder

client = Payrudder()

try:
    customer = client.customers.create_customer(
        {"external_id": "cus_42", "name": "Ada Lovelace", "email": "ada@example.com"}
    )
    print("created", customer["external_id"])
except ConflictError:
    print("cus_42 exists already")
