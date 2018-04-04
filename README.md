# s3copy
A lambda function to copy objects from n to m S3 Buckets
 where n, m >= 1
 
 
## Deployment
- Download the deployment file from 
https://github.com/maknahar/s3copy/releases
- Upload the deployment file to lambda
- Set the configuration

or

- Checkout the latest release code 
`go get github.com/maknahar/copys3`
- Build and generate the deployment file 
`GOOS=linux go build -o main main.go && zip deployment.zip main`
- Upload the deployment file to lambda
- Set the configuration 

**Handler function name:** `main`

## Config
This Lambda Function expect the list of source and 
destination buckets in a JSON file.


### Config File Format  

Config File is a map of input s3 bucket and detail to copy 
changes to destination.

The function can take events from an SQS as well if 
source bucket put all event to an SQS.

The function can take events from an SQS where SQS is 
subscribed to an SNS and source bucket publish a message 
to an SNS on every change.

To Support SQS processing add SQS URL `sqs` and
SQS region `sqsRegion` in the config file. Otherwise,
Leave them empty `""` or remove the keys.

```
{
  "source-bucket": {
    "region": "us-east-1",
    "sqs": "URL of SQS where all S3 change events are stored",
    "sqsRegion": "us-east-1",
    "destinations": [
      "destination-bucket-1",
      "destination-bucket-2"
    ]
  },
  "another-source-bucket": {
    "region": "us-east-1",
    "sqs": "SQS URL",
    "sqsRegion": "us-east-1",
    "destinations": [
      "destination-bucket-1",
      "destination-bucket-3"
    ]
  }
}
```

With above configuration, any object put in `source-bucket` 
will be copied to `destination-bucket-1` & `destination-bucket-2`
and any object put in `another-source-bucket` will be 
copied to `destination-bucket-1` & `destination-bucket-3`.


### Env Var
This Lambda function can be configured in two ways. 
- Either give a public url of config file via CONFIG_FILE
- or provide base64 encoded value of configuration via 
CONFIG. You can online tools like www.base64encode.org
to encode config content.

If both are provided CONFIG_FILE value will take precedence.

- CONFIG_FILE : URL of configuration file

- CONFIG : Base64 Encoded string of content of CONFIG_FILE

## NOTE:
Make sure Lambda function have required access to source 
and destination bucket. 

## Contribution
All contributions are welcome. Either via PR or Issue.