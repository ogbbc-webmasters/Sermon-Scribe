module View exposing (view)

import Api exposing (Sermon)
import DateFormat exposing (formatDate)
import Editor
import File
import Html exposing (Html, a, audio, button, div, h1, h2, input, label, p, span, strong, text)
import Html.Attributes exposing (accept, attribute, class, controls, disabled, download, href, id, src, style, title, type_)
import Html.Events exposing (on, onClick, stopPropagationOn)
import Json.Decode as Decode exposing (Decoder)
import Set
import Types exposing (Model, Msg(..), SermonList(..), UploadState(..))
import Ui


view : Model -> Html Msg
view model =
    case model.editing of
        Just editor ->
            Html.map EditorMsg (Editor.view editor)

        Nothing ->
            case model.selectedSermon of
                Just sermon ->
                    let
                        current =
                            case model.sermons of
                                Loaded sermons ->
                                    List.filter (\candidate -> candidate.id == sermon.id) sermons
                                        |> List.head
                                        |> Maybe.withDefault sermon

                                _ ->
                                    sermon
                    in
                    viewSermonDetail model current

                Nothing ->
                    div [ class "page" ]
                        [ div [ class "masthead" ]
                            [ h1 [] [ text "Sermon Scribe" ] ]
                        , viewUpload model.upload
                        , h2 [] [ text "Sermons" ]
                        , viewOptionalError model.deleteError
                        , viewOptionalError model.normalizationError
                        , viewSermons model
                        ]


viewSermonDetail : Model -> Sermon -> Html Msg
viewSermonDetail model sermon =
    div [ class "page" ]
        [ div [ class "masthead" ]
            [ h1 [] [ text "Sermon Scribe" ] ]
        , div [ class "sermon-detail" ]
            [ div [ class "sermon-detail__header" ]
                [ button [ Ui.button, onClick CloseSermon ] [ text "← Back to Sermons" ]
                , div [ class "sermon-detail__identity" ]
                    [ h2 [ class "sermon-detail__title" ] [ text (Maybe.withDefault "Title Unknown" sermon.title) ]
                    , p [ class "sermon-detail__filename" ] [ text sermon.originalFilename ]
                    , case sermon.speaker of
                        Just speaker ->
                            p [ class "sermon-detail__speaker" ] [ text ("Speaker: " ++ speaker) ]

                        Nothing ->
                            text ""
                    , p [ Ui.cardMeta ]
                        [ span [ badgeAttribute sermon ] [ text (describeStage sermon) ]
                        , text (formatDate model.zone sermon.uploadedAt)
                        ]
                    ]
                ]
            , viewDetailStatus sermon
            , viewDetailMetadata sermon
            , viewDetailTranscript sermon
            , div [ class "sermon-detail__footer" ]
                [ viewOptionalError model.deleteError
                , viewOptionalError model.retryError
                , viewDetailActions model sermon
                ]
            ]
        ]


viewDetailStatus : Sermon -> Html Msg
viewDetailStatus sermon =
    case sermon.status of
        "failed" ->
            div [ Ui.errorPanel ]
                [ strong [ Ui.panelTitle ] [ text "Processing failed" ]
                , p [ Ui.panelText ] [ text "Please retry or contact an administrator." ]
                ]

        "done" ->
            text ""

        _ ->
            div [ Ui.panel ]
                [ strong [ Ui.panelTitle ] [ text (describeStage sermon) ]
                , p [ Ui.panelText ] [ text "This sermon is still being processed." ]
                ]


viewDetailMetadata : Sermon -> Html Msg
viewDetailMetadata sermon =
    div [ class "sermon-detail__sections" ]
        (List.concat
            [ case sermon.scriptures of
                [] ->
                    []

                scriptures ->
                    [ div [ Ui.panel ]
                        [ strong [ Ui.panelTitle ] [ text "Scripture References" ]
                        , div [ class "sermon-detail__pills" ]
                            (List.map (\scripture -> span [ class "sermon-detail__pill sermon-detail__pill--scripture" ] [ text scripture ]) scriptures)
                        ]
                    ]
            , if List.isEmpty (highConfidenceTopics sermon.topicScores) then
                []

              else
                [ div [ Ui.panel ]
                    [ strong [ Ui.panelTitle ] [ text "Topics" ]
                    , div [ class "sermon-detail__pills" ]
                        (List.map
                            (\( topic, score ) ->
                                span [ class "sermon-detail__pill sermon-detail__pill--topic" ]
                                    [ text (topic ++ " " ++ String.fromInt (round (score * 100)) ++ "%") ]
                            )
                            (highConfidenceTopics sermon.topicScores)
                        )
                    ]
                ]
            ]
        )


viewDetailTranscript : Sermon -> Html Msg
viewDetailTranscript sermon =
    div [ Ui.panel ]
        [ strong [ Ui.panelTitle ] [ text "Transcript" ]
        , case sermon.transcript of
            Just transcript ->
                p [ class "sermon-detail__transcript" ] [ text transcript ]

            Nothing ->
                p [ Ui.panelText ] [ text "Transcript not available yet." ]
        ]


highConfidenceTopics : List ( String, Float ) -> List ( String, Float )
highConfidenceTopics scores =
    scores
        |> List.filter (\( _, score ) -> score >= 0.8)
        |> List.sortBy (\( _, score ) -> -score)


viewDetailActions : Model -> Sermon -> Html Msg
viewDetailActions model sermon =
    case model.confirmingDelete of
        Just pending ->
            if pending.id == sermon.id then
                div [ Ui.confirmBox ]
                    [ p [ Ui.confirmBoxQuestion ]
                        [ strong [] [ text "Delete this sermon and its audio files?" ] ]
                    , div [ Ui.confirmBoxButtons ]
                        [ button [ Ui.dangerButton, onClick (ConfirmDelete sermon) ] [ text "Yes, Delete" ]
                        , button [ Ui.button, onClick CancelDelete ] [ text "Cancel" ]
                        ]
                    ]

            else
                viewDetailActionButtons model sermon

        Nothing ->
            viewDetailActionButtons model sermon


viewDetailActionButtons : Model -> Sermon -> Html Msg
viewDetailActionButtons model sermon =
    div [ Ui.sermonActions ]
        (List.concat
            [ if sermon.status == "failed" then
                [ button
                    [ Ui.button
                    , onClick (RetrySermon sermon)
                    , disabled (Set.member sermon.id model.retrying || Set.member sermon.id model.deleting)
                    ]
                    [ text
                        (if Set.member sermon.id model.retrying then
                            "Retrying…"

                         else
                            "Retry"
                        )
                    ]
                ]

              else
                []
            , [ button
                    [ Ui.dangerButton
                    , onClick (AskDelete sermon)
                    , disabled (Set.member sermon.id model.deleting || Set.member sermon.id model.retrying)
                    ]
                    [ text
                        (if Set.member sermon.id model.deleting then
                            "Deleting…"

                         else
                            "Delete Sermon"
                        )
                    ]
              ]
            ]
        )


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
                    [ text ("Uploading… " ++ percent fraction) ]
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
            p [ Ui.hint ] [ text "Loading…" ]

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
    div
        [ Ui.card
        , on "click"
            (if model.confirmingDelete /= Nothing || Set.member sermon.id model.deleting then
                Decode.fail "Card navigation is disabled"

             else
                Decode.succeed (OpenSermon sermon)
            )
        ]
        [ div [ Ui.cardInfo ]
            [ p [ Ui.cardName ] [ text (Maybe.withDefault "Title Unknown" sermon.title) ]
            , p [ Ui.cardMeta ]
                [ span [ badgeAttribute sermon ] [ text (describeStage sermon) ]
                , text (formatDate model.zone sermon.uploadedAt)
                ]
            , div [ stopPropagationOn "click" (Decode.succeed ( NoOp, True )) ] [ viewSermonActions model sermon ]
            ]
        , button
            [ Ui.quietIconButton
            , stopPropagationOn "click" (Decode.succeed ( OpenSermon sermon, True ))
            , disabled (model.confirmingDelete /= Nothing || Set.member sermon.id model.deleting)
            , attribute "aria-label" "Open Sermon"
            , title "Open Sermon"
            ]
            [ Ui.icon "ph:caret-right" ]
        , viewNormalizedAudio model sermon
        ]


viewNormalizedAudio : Model -> Sermon -> Html Msg
viewNormalizedAudio model sermon =
    if sermon.stage == "normalization" && sermon.status == "done" && not sermon.normalizationReviewed then
        let
            isBusy =
                Set.member sermon.id model.rerunning
                    || Set.member sermon.id model.reviewingNormalization
                    || Set.member sermon.id model.deleting
                    || Set.member sermon.id model.retrying

            adjustmentButtons =
                normalizationAdjustments
                    |> List.map
                        (\adjustment ->
                            button
                                [ Ui.button
                                , onClick (RerunNormalization sermon adjustment.name)
                                , disabled (isBusy || adjustmentAtLimit sermon adjustment.name)
                                ]
                                [ text adjustment.label ]
                        )
        in
        div [ Ui.audioReview, stopPropagationOn "click" (Decode.succeed ( NoOp, True )) ]
            [ audio
                [ Ui.audioReviewPlayer
                , controls True
                , src (normalizedAudioUrl sermon "proxy")
                ]
                []
            , p [ Ui.audioReviewLabel ]
                [ text "How does the recording sound?" ]
            , div [ Ui.audioReviewControls ]
                (button
                    [ Ui.primaryButton
                    , onClick (ReviewNormalization sermon)
                    , disabled isBusy
                    ]
                    [ text
                        (if Set.member sermon.id model.reviewingNormalization then
                            "Continuing…"

                         else
                            "Continue"
                        )
                    ]
                    :: adjustmentButtons
                )
            , p [ Ui.hint ]
                [ a
                    [ class "focusable"
                    , href (normalizedAudioUrl sermon "proxy" ++ "&download=1")
                    , download "normalized.mp3"
                    ]
                    [ text "Download MP3" ]
                ]
            ]

    else
        text ""


type alias NormalizationAdjustment =
    { name : String
    , label : String
    }


normalizationAdjustments : List NormalizationAdjustment
normalizationAdjustments =
    [ { name = "more-gate", label = "I hear too much background noise" }
    , { name = "less-gate", label = "Some words sound cut off" }
    , { name = "more-volume", label = "The recording is too quiet" }
    , { name = "less-volume", label = "The recording is too loud" }
    ]


adjustmentAtLimit : Sermon -> String -> Bool
adjustmentAtLimit sermon adjustment =
    case adjustment of
        "more-gate" ->
            sermon.normalizationGateAdjustment >= 3

        "less-gate" ->
            sermon.normalizationGateAdjustment <= -3

        "more-volume" ->
            sermon.normalizationVolumeAdjustment >= 3

        "less-volume" ->
            sermon.normalizationVolumeAdjustment <= -3

        _ ->
            True


audioUrl : String -> String -> String
audioUrl id audioType =
    "/api/sermons/" ++ id ++ "/audio/" ++ audioType


normalizedAudioUrl : Sermon -> String -> String
normalizedAudioUrl sermon audioType =
    audioUrl sermon.id audioType
        ++ "?gate="
        ++ String.fromInt sermon.normalizationGateAdjustment
        ++ "&volume="
        ++ String.fromInt sermon.normalizationVolumeAdjustment


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
        isDeleting =
            Set.member sermon.id model.deleting
    in
    div [ Ui.sermonActions ]
        (List.concat
            [ if (sermon.stage == "edit" || (sermon.stage == "normalization" && sermon.status == "done" && sermon.normalizationReviewed)) && not sermon.editApproved then
                [ button [ Ui.primaryButton, onClick (OpenEditor sermon), disabled (confirmationOpen || isDeleting) ]
                    [ text
                        (if sermon.stage == "edit" && sermon.status == "done" then
                            "Review Final"

                         else
                            "Open Editor"
                        )
                    ]
                ]

              else
                []
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
            "Uploading…"

        ( "upload", "done" ) ->
            "Uploaded"

        ( "normalization", "pending" ) ->
            "Waiting to normalize"

        ( "normalization", "running" ) ->
            if sermon.progress < 0 then
                "Normalizing…"

            else
                "Normalizing… " ++ String.fromInt sermon.progress ++ "%"

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
