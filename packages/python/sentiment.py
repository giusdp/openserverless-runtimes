# Licensed to the Apache Software Foundation (ASF) under one
# or more contributor license agreements.  See the NOTICE file
# distributed with this work for additional information
# regarding copyright ownership.  The ASF licenses this file
# to you under the Apache License, Version 2.0 (the
# "License"); you may not use this file except in compliance
# with the License.  You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

#--web true
#--kind python:default
    
from subprocess import run

def setup(args, status):
    status.append("installing torch")
    run(["pip", "install", "torch"])
    status.append("installing transformers")
    run(["pip", "install", "transformers"])
    status.append("loading transformers")
    from transformers import pipeline
    pipeline("sentiment-analysis")

def main(args):
    if "setup_status" in args:
        res = "\n".join(args['setup_status'])
        return { "body": res }
    
    from transformers import pipeline
    sentiment = pipeline('sentiment-analysis')
    input = args.get("input", "")
    if input == "":
        return { "body": "please provide some input"}
    output = sentiment(input)
    return {
        "body": output
    }

