module View exposing (view)

import Api exposing (Sermon)
import DateFormat exposing (formatDate)
import File
import Html exposing (Html, a, audio, button, div, h1, h2, input, label, p, span, strong, text)
import Html.Attributes exposing (accept, class, controls, disabled, download, href, id, src, style, type_)
import Html.Events exposing (on, onClick)
import Json.Decode as Decode exposing (Decoder)
import Set
import Types exposing (Model, Msg(..), SermonList(..), UploadState(..))
import Ui


view : Model -> Html Msg
view model =
    div [ class "page" ]
        [ div [ class "masthead" ]
            [ h1 [] [ text "Sermon Scribe" ] ]
        , viewUpload model.upload
        , h2 [] [ text "Sermons" ]
        , viewOptionalError model.deleteError
        , viewOptionalError model.retryError
        , viewOptionalError model.normalizationError
        , viewSermons model
        ]


viewOptionalError : Maybe String -> Html Msg
viewOptionalError maybeMessage =
    case maybeMessage of
        Just message ->
            p [ Ui.errorText ] [ text message ]

        Nothing ->
            text ""


viewUpload : UploadState -> Html Msg
viewUpload upload =
    case upload of
        Uploading fraction ->
            div [ class "upload-box" ]
                [ p [ class "upload-box__status" ]
                    [ text ("Uploading\u{2026} " ++ percent fraction) ]
                , viewProgressBar fraction
                , p [ Ui.hint ]
                    [ text "Please keep this page open until the upload finishes." ]
                ]

        _ ->
            div [ class "upload-box" ]
                (List.concat
                    [ [ label [ Ui.primaryButton, Html.Attributes.for "file-input" ]
                            [ text "Upload a Sermon Recording" ]
                      , input
                            [ type_ "file"
                            , id "file-input"
                            , accept "audio/*"
                            , on "change" fileChangeDecoder
                            , style "display" "none"
                            ]
                            []
                      , p [ Ui.hint ]
                            [ text "Click the button to choose the audio file from your computer." ]
                      ]
                    , case upload of
                        UploadFailed message ->
                            [ p [ Ui.errorText ] [ text message ] ]

                        _ ->
                            []
                    ]
                )


fileChangeDecoder : Decoder Msg
fileChangeDecoder =
    Decode.at [ "target", "files" ] (Decode.index 0 File.decoder)
        |> Decode.map FilePicked


viewProgressBar : Float -> Html Msg
viewProgressBar fraction =
    div [ Ui.progress ]
        [ div
            [ Ui.progressFill
            , style "width" (percent fraction)
            ]
            []
        ]


percent : Float -> String
percent fraction =
    String.fromInt (round (fraction * 100)) ++ "%"


viewSermons : Model -> Html Msg
viewSermons model =
    case model.sermons of
        Loading ->
            p [ Ui.hint ] [ text "Loading\u{2026}" ]

        LoadFailed ->
            p [ Ui.errorText ]
                [ text "Could not load the sermon list. Please reload the page." ]

        Loaded [] ->
            div [ Ui.emptyState ]
                [ text "No sermons yet. Upload one to get started." ]

        Loaded sermons ->
            div [] (List.map (viewSermon model) sermons)


viewSermon : Model -> Sermon -> Html Msg
viewSermon model sermon =
    div [ Ui.card ]
        [ div [ Ui.cardInfo ]
            [ p [ Ui.cardName ] [ text sermon.originalFilename ]
            , p [ Ui.cardMeta ]
                [ span [ badgeAttribute sermon ] [ text (describeStage sermon) ]
                , text (formatDate model.zone sermon.uploadedAt)
                ]
            , case ( sermon.status, sermon.error ) of
                ( "failed", Just message ) ->
                    p [ Ui.errorText ] [ text message ]

                _ ->
                    text ""
            ]
        , viewNormalizedAudio model sermon
        , viewSermonActions model sermon
        ]


viewNormalizedAudio : Model -> Sermon -> Html Msg
viewNormalizedAudio model sermon =
    if sermon.stage == "normalization" && sermon.status == "done" then
        let
            isBusy =
                Set.member sermon.id model.rerunning
                    || Set.member sermon.id model.deleting
                    || Set.member sermon.id model.retrying

            adjustmentButtons =
                normalizationAdjustments
                    |> List.map
                        (\( preset, labelText ) ->
                            button
                                [ Ui.button
                                , onClick (RerunNormalization sermon preset)
                                , disabled (isBusy || preset == sermon.normalizationPreset)
                                ]
                                [ text labelText ]
                        )
        in
        div [ Ui.audioReview ]
            [ audio
                [ Ui.audioReviewPlayer
                , controls True
                , src (audioUrl sermon.id "proxy")
                ]
                []
            , p [ Ui.audioReviewLabel ]
                [ text ("Current preset: " ++ presetLabel sermon.normalizationPreset) ]
            , div [ Ui.audioReviewControls ]
                (a
                    [ Ui.primaryButton
                    , href (audioUrl sermon.id "proxy" ++ "?download=1")
                    , download "normalized.mp3"
                    ]
                    [ text "Continue" ]
                    :: adjustmentButtons
                )
            , p [ Ui.hint ] [ text "Continue downloads the normalized MP3." ]
            ]

    else
        text ""


normalizationAdjustments : List ( String, String )
normalizationAdjustments =
    [ ( "stronger-gate", "I hear too much background noise" )
    , ( "no-gate", "Some words sound cut off" )
    , ( "louder", "The recording is too quiet" )
    , ( "quieter", "The recording is too loud" )
    ]


presetLabel : String -> String
presetLabel preset =
    case preset of
        "standard" ->
            "Standard"

        "stronger-gate" ->
            "Stronger noise gate"

        "no-gate" ->
            "No noise gate"

        "louder" ->
            "Louder"

        "quieter" ->
            "Quieter"

        _ ->
            capitalize preset


audioUrl : String -> String -> String
audioUrl id audioType =
    "/api/sermons/" ++ id ++ "/audio/" ++ audioType


viewSermonActions : Model -> Sermon -> Html Msg
viewSermonActions model sermon =
    case model.confirmingDelete of
        Just pending ->
            if pending.id == sermon.id then
                div [ Ui.confirmBox ]
                    [ p [ Ui.confirmBoxQuestion ]
                        [ strong [] [ text "Delete this sermon and its audio files?" ] ]
                    , div [ Ui.confirmBoxButtons ]
                        [ button
                            [ Ui.dangerButton, onClick (ConfirmDelete sermon) ]
                            [ text "Yes, Delete" ]
                        , button
                            [ Ui.button, onClick CancelDelete ]
                            [ text "Cancel" ]
                        ]
                    ]

            else
                viewActionButtons model sermon True

        Nothing ->
            viewActionButtons model sermon False


viewActionButtons : Model -> Sermon -> Bool -> Html Msg
viewActionButtons model sermon confirmationOpen =
    let
        isRetrying =
            Set.member sermon.id model.retrying

        isDeleting =
            Set.member sermon.id model.deleting
    in
    div [ Ui.sermonActions ]
        (List.concat
            [ if sermon.status == "failed" then
                [ button
                    [ Ui.button
                    , onClick (RetrySermon sermon)
                    , disabled (confirmationOpen || isRetrying || isDeleting)
                    ]
                    [ text
                        (if isRetrying then
                            "Retrying\u{2026}"

                         else
                            "Retry"
                        )
                    ]
                ]

              else
                []
            , [ button
                    [ Ui.button
                    , onClick (AskDelete sermon)
                    , disabled (confirmationOpen || isRetrying || isDeleting)
                    ]
                    [ text
                        (if isDeleting then
                            "Deleting\u{2026}"

                         else
                            "Delete"
                        )
                    ]
              ]
            ]
        )


{-| Render a stage/status pair in plain language. Later specs add more
stages; unknown combinations fall back to a generic rendering.
-}
badgeAttribute : Sermon -> Html.Attribute Msg
badgeAttribute sermon =
    if sermon.status == "failed" then
        Ui.badgeFailed

    else
        Ui.badge


describeStage : Sermon -> String
describeStage sermon =
    case ( sermon.stage, sermon.status ) of
        ( "upload", "pending" ) ->
            "Waiting to upload"

        ( "upload", "running" ) ->
            "Uploading\u{2026}"

        ( "upload", "done" ) ->
            "Uploaded"

        ( "normalization", "pending" ) ->
            "Waiting to normalize"

        ( "normalization", "running" ) ->
            if sermon.progress < 0 then
                "Normalizing\u{2026}"

            else
                "Normalizing\u{2026} " ++ String.fromInt sermon.progress ++ "%"

        ( "normalization", "done" ) ->
            "Normalization finished"

        ( "normalization", "failed" ) ->
            "Normalization failed"

        ( stage, "done" ) ->
            capitalize stage ++ " finished"

        ( stage, "running" ) ->
            capitalize stage ++ " in progress"

        ( stage, "pending" ) ->
            capitalize stage ++ " waiting"

        ( stage, "failed" ) ->
            capitalize stage ++ " failed"

        ( stage, status ) ->
            capitalize stage ++ ": " ++ status


capitalize : String -> String
capitalize s =
    case String.uncons s of
        Just ( first, rest ) ->
            String.cons (Char.toUpper first) rest

        Nothing ->
            s
