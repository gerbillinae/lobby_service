import unittest
import requests
import json
import time
import argparse
import sys
from typing import Any
from collections.abc import Iterator

def read_sse(gen : Iterator[str]):
    buffer = ""
    for line in gen:
        if line.strip() == "":
            buffer += "\n"
            break
        buffer += line + "\n"
    return buffer

# Parse N sse messages with json payloads
def load_sse_json_data(msg : str) -> list[Any]:
    entries = []

    event = ""
    data = ""
    for line in msg.splitlines():
        if line.startswith("data:"):
            data += line[5:]
        if line.startswith("event:"):
            if event != "":
                raise ValueError("Invalid input")
            event = line[6:]
        elif len(line) == 0:
            entries.append({"event": event, "data": json.loads(data)})
            event = ""
            data = ""

    return entries

class Test(unittest.TestCase):

    address : str = ""
    # Basic test of the happy-path
    def test_expected_path(self):

        # URL to send the POST request
        url = Test.address

        # Wait for server to be ready
        for i in range(5):
            try:
                ping_response = requests.get(url+"/ping")
                if ping_response.status_code == 200:
                    break
            except:
                pass
            time.sleep(1000)


        #print("CREATE")
        # Data to be sent in the POST request
        create_request = {
            "name": "admin",
            "creation_info": "CREATION_INFO"
        }

        create_response = requests.post(url+"/create", json=create_request)

        self.assertEqual(create_response.status_code, 200)
        creation_data = json.loads(create_response.text)
        self.assertEqual(creation_data["status"], "success")

        #print("GET EVENTS")
        events_request = {
            "room": creation_data["room"],
            "token": creation_data["token"]
        }

        # Open a streaming connection to the server
        events_response = requests.get(url+"/events", events_request, stream=True)
        events_generator = events_response.iter_lines(decode_unicode=True)

        # Check if the server supports SSE
        self.assertEqual(events_response.headers.get("Content-Type"), "text/event-stream")
        self.assertEqual(events_response.headers.get("Cache-Control"), "no-cache")
        self.assertEqual(events_response.headers.get("Connection"), "keep-alive")
        self.assertEqual(events_response.headers.get("Transfer-Encoding"), "chunked")

        #print("JOIN")
        # When a join request is made ...
        join_request = {
            "name": "Mario",
            "room": creation_data["room"]
        }

        join_response = requests.post(url+"/join", json=join_request)
        self.assertEqual(join_response.status_code, 200)
        join_data = json.loads(join_response.text)
        self.assertEqual(join_data["status"], "success")

        self.assertEqual(len(join_data["token"]), 36)
        self.assertEqual(join_data["id"], 1)
        user_1_token = join_data["token"]

        # Then a join event is recieved for the new user
        join_event = load_sse_json_data(read_sse(events_generator))
        self.assertEqual(len(join_event), 1)
        self.assertEqual(join_event[0]["event"], "user_added")
        self.assertEqual(join_event[0]["data"]["id"], 1)
        self.assertEqual(join_event[0]["data"]["name"], join_request["name"])

        # Then an info request shows the new user
        info_request = {
            "room": creation_data["room"],
            "token": join_data["token"]
        }

        info_response = requests.get(url+"/info", info_request)

        self.assertEqual(info_response.status_code, 200)
        info_data = json.loads(info_response.text)

        usernames = [user["name"] for user in info_data["info"]["users"] ]
        self.assertTrue(create_request["name"] in usernames)
        self.assertTrue(join_request["name"] in usernames)

        self.assertTrue("creation_info" in info_data["info"])

        # Info requests should not show completion info until the room is complete
        self.assertTrue("completion_info" not in info_data["info"])

        rename_request = {
            "token": join_data["token"],
            "room": creation_data["room"],
            "name": "Luigi"
        }

        rename_response = requests.post(url+"/name", json=rename_request)
        self.assertEqual(rename_response.status_code, 200)
        rename_data = json.loads(rename_response.text)
        self.assertEqual(rename_data["status"], "success")

        # A rename event is recieved
        rename_event = load_sse_json_data(read_sse(events_generator))
        self.assertEqual(len(rename_event), 1)
        self.assertEqual(rename_event[0]["event"], "user_renamed")
        self.assertEqual(rename_event[0]["data"]["id"], 1)
        self.assertEqual(rename_event[0]["data"]["name"], rename_request["name"])

        info_request = {
            "room": creation_data["room"],
            "token": join_data["token"]
        }

        info_response = requests.get(url+"/info", info_request)

        self.assertEqual(info_response.status_code, 200)
        info_data = json.loads(info_response.text)

        usernames = [user["name"] for user in info_data["info"]["users"]]
        self.assertTrue(create_request["name"] in usernames)
        self.assertTrue(rename_request["name"] in usernames)
        self.assertTrue(join_request["name"] not in usernames)

        complete_request = {
            "token": join_data["token"],
            "room": creation_data["room"],
            "completion_info": "COMPLETION_INFO"
        }

        complete_response = requests.post(url+"/complete", json=complete_request)

        self.assertEqual(complete_response.status_code, 400)
        complete_data = json.loads(complete_response.text)
        self.assertEqual(complete_data["error"], "Permission denied")

        complete_request = {
            "token": creation_data["token"],
            "room": creation_data["room"],
            "completion_info": "COMPLETION_INFO"
        }

        complete_response = requests.post(url+"/complete", json=complete_request)

        # Then a join event is recieved for the new user
        complete_event = load_sse_json_data(read_sse(events_generator))
        self.assertEqual(len(complete_event), 1)
        self.assertEqual(complete_event[0]["event"], "complete")
        self.assertEqual(complete_event[0]["data"]["message_type"], "complete")
        self.assertEqual(complete_event[0]["data"]["completion_info"], complete_request["completion_info"])

        # Confirm event stream is closed by the server after a "complete" message
        for line in events_generator:
            self.assertTrue(False)

        self.assertEqual(complete_response.status_code, 200)
        complete_data = json.loads(complete_response.text)
        self.assertEqual(complete_data["status"], "success")

        info_response = requests.get(url+"/info", info_request)

        self.assertEqual(info_response.status_code, 200)
        info_data = json.loads(info_response.text)

        self.assertTrue("creation_info" in info_data["info"])
        self.assertEqual(info_data["info"]["creation_info"], create_request["creation_info"])
        self.assertEqual(info_data["info"]["completion_info"], complete_request["completion_info"])

        events_response.close()

        # Server can no longer be joined
        join_request = {
            "name": "Peach",
            "room": creation_data["room"]
        }

        join_response = requests.post(url+"/join", json=join_request)
        self.assertEqual(join_response.status_code, 400)
        join_data = json.loads(join_response.text)
        self.assertTrue("error" in join_data)


        # Users can no longer rename
        rename_request = {
            "token": user_1_token,
            "room": creation_data["room"],
            "name": "Luigi"
        }

        rename_response = requests.post(url+"/name", json=rename_request)
        self.assertEqual(rename_response.status_code, 400)
        rename_data = json.loads(rename_response.text)
        self.assertTrue("error" in rename_data)

        time.sleep(11)
        info_response = requests.get(url+"/info", info_request)
        self.assertEqual(info_response.status_code, 400)

if __name__ == "__main__":
    # Create an argument parser
    parser = argparse.ArgumentParser(description="A script that processes CLI arguments.")

    # Add arguments
    parser.add_argument("--address", type=str, required=True, help="URL or IP of the server")

    # Parse the arguments
    args, unknown_args = parser.parse_known_args()

    # Access the arguments
    print("Address:", args.address)
    Test.address = args.address

    unittest.main(argv=[sys.argv[0]]+unknown_args)
