package cognito

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider"
	"github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider/types"
	"github.com/fatih/color"
	"gtech.dev/spark/config"
)

func callCognitoInitiateAuth(client cognitoidentityprovider.Client, input cognitoidentityprovider.InitiateAuthInput, firstLogin bool) *cognitoidentityprovider.InitiateAuthOutput {
	response, err := client.InitiateAuth(context.TODO(), &input)
	if err != nil {
		// We know this error, and so we want to return a simple response to the terminal
		if strings.Contains(err.Error(), "Incorrect username or password") {
			color.Red("Invalid username and/or password")
			// If password reset is required, then we return a special response
		} else if strings.Contains(err.Error(), "PasswordResetRequiredException") {
			return &cognitoidentityprovider.InitiateAuthOutput{
				AuthenticationResult: nil,
				ChallengeName:        types.ChallengeNameTypeNewPasswordRequired,
			}
		} else {
			color.Red("Error authenticating to Cognito: %v", err.Error())
		}
		os.Exit(1)
	}

	// This happens when the user has not performed an initial login
	if response.ChallengeName == types.ChallengeNameTypeNewPasswordRequired && !firstLogin {
		color.Red("User has not performed initial login, please perform initial login")
		os.Exit(1)
	}
	return response
}

func initiateAuth(client cognitoidentityprovider.Client, configuration *config.CognitoConfig, username string, password string, force bool) {
	now := time.Now()
	if config, err := config.GetCognitoConfig(); err != nil {
		color.Red("Error getting config: %v", err.Error())
		os.Exit(1)
	} else {
		// Don't update if the token is valid for 6 hours or greater
		timeLeft := time.Until(time.Unix(config.Expires, 0))
		if timeLeft.Hours() >= 6 && !force {
			color.Cyan("Token is still valid for %v hours, not updating", int(timeLeft.Hours()))
			os.Exit(0)
		}
	}

	if username == "" {
		username = GetUsername()
	}

	if password == "" {
		password = GetPassword()
	}

	input := cognitoidentityprovider.InitiateAuthInput{
		AuthFlow: types.AuthFlowTypeUserPasswordAuth,
		AuthParameters: map[string]string{
			"USERNAME": username,
			"PASSWORD": password,
		},
		ClientId: aws.String(configuration.ClientId),
	}

	response := callCognitoInitiateAuth(client, input, false)

	for {
		if response.ChallengeName == "" {
			break
		}

		switch response.ChallengeName {
		case types.ChallengeNameTypeNewPasswordRequired:
			color.Cyan("New password required, initiating password reset")
			newPassword := forgotPassword(client, configuration, username)
			input.AuthParameters["PASSWORD"] = newPassword
			response = callCognitoInitiateAuth(client, input, false)
		case types.ChallengeNameTypeMfaSetup:
			color.Cyan("You have not configured an OTP device, initiating OTP device setup. Please note that a code may only be used once. Attempting to use the code more than once will result in failure.")
			registerTotp(client, *response.Session)
			response = callCognitoInitiateAuth(client, input, false)
			color.Cyan("OTP device registered, performing authentication")
		case types.ChallengeNameTypeSoftwareTokenMfa:
			otp := getOtp()
			challengeResponse := respondToAuthChallenge(client, cognitoidentityprovider.RespondToAuthChallengeInput{
				ChallengeName: types.ChallengeNameTypeSoftwareTokenMfa,
				ChallengeResponses: map[string]string{
					"USERNAME":                username,
					"SOFTWARE_TOKEN_MFA_CODE": *aws.String(otp),
				},
				ClientId: &configuration.ClientId,
				Session:  response.Session,
			})
			response = &cognitoidentityprovider.InitiateAuthOutput{
				AuthenticationResult: challengeResponse.AuthenticationResult,
				ChallengeName:        challengeResponse.ChallengeName,
				ChallengeParameters:  challengeResponse.ChallengeParameters,
				Session:              response.Session,
			}
		}
	}

	var userSession string

	// Session may be null if there's not challenge
	if response.Session == nil {
		userSession = ""
	} else {
		userSession = *response.Session
	}

	cognitoConfig := config.CognitoConfig{
		ClientId:    configuration.ClientId,
		Region:      configuration.Region,
		PoolId:      configuration.PoolId,
		AccessToken: *response.AuthenticationResult.AccessToken,
		IdToken:     *response.AuthenticationResult.IdToken,
		Expires:     now.Add(time.Second * time.Duration(response.AuthenticationResult.ExpiresIn)).Unix(),
		Session:     userSession,
	}

	if err := config.UpdateCognitoConfig(cognitoConfig); err != nil {
		color.Red("Error updating session: %v", err.Error())
		os.Exit(1)
	}

	color.Green("Successfully updated session")
}
