# docs2html

## !! IMPORTANT !!
This is only for educational purpose. You can achieve mostly the same result simpler solutions: either [use Google
Syntetic records](https://support.google.com/domains/answer/6069273?hl=en) if your registrar is Google Domains, or
[use an S3 bucket](https://dev.to/marklocklear/redirecting-a-domain-with-https-using-amazon-s3-and-cloudfront-526h)

I wanted to show my Google Doc resume as my landing page. For that I wanted to use an AWS Lambda because:
1. I wanted to play around with AWS (I'm using Azure these days)
2. I wanted to write some Go

The idea is: write an AWS Lambda that upon a push notification from the Google Drive API will export
the specified Google Doc as an HTML file, push the file to GitHub and let it serve as a GitHub Page

* Create an empty Go lambda
* Make Route53 manage a subdomain for the domain you control
* Issue a TLS certificate using ACM
* Use a CNAME record to prove you control the domain
* Create an API Gateway in front of the lambda function
* Setup a custom domain for the API Gateway using the certificate above
* Set the custom domain to point to the API Gateway in front of the lambda
* Copy the API Gateway domain name and make the subdomain point to that domain in Route53 using an A record (use an AWS alias)
* You can test everything is working for now by opening the browser at the subdomain and get the "Hello from Lambda!" message
* Create a new project from the Google Cloud Platform website
* Enable the Google Drive API to be used by the project
* Add the domain serving the lambda to the list of allowed domains
* Generate credentials for the lambda to use the Google Drive API: create a Service Account and generate a private key
* From the Google Doc UI, share the document with the Service Account email
* Build `main.go` and upload the binary to AWS
* Generate an SSH keypair and add the pubkey to GitHub
* Create a repository with GitHub Pages enabled
* Setup the lambda env variables needed to register the lambda to receive notifications when an update occurs
* Add an Amazon EventBridge as a trigger to the lambda: let it fire an event once per hour such that the Google Drive API will keep on sending notifications (the subscription expires after 1 hour by default)
* Modify the Google Doc, wait a couple of minutes and see it changing the website
