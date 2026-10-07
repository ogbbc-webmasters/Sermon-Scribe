module View exposing (view)

import Api exposing (Sermon)
import Card
import DateFormat exposing (formatDate)
import Dict
import File
import Html exposing (Html, a, audio, button, details, div, h1, h2, input, label, mark, p, span, strong, summary, text)
import Html.Attributes exposing (accept, attribute, class, controls, disabled, download, hidden, href, id, placeholder, src, style, title, type_, value)
import Html.Events exposing (on, onClick, onInput, stopPropagationOn)
import Icon
import Json.Decode as Decode exposing (Decoder)
import Set
import Types exposing (Model, Msg(..), SermonList(..), UploadState(..))
import Ui


view : Model -> Html Msg
view model =
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
    let
        hasMetadata =
            sermon.transcript
                /= Nothing
                || sermon.title
                /= Nothing
                || sermon.titleReasoning
                /= Nothing
                || not (List.isEmpty sermon.scriptures)
                || not (List.isEmpty (highConfidenceTopics sermon.topicScores))
    in
    div [ class "page page--detail" ]
        [ div [ class "masthead" ]
            [ h1 [] [ text "Sermon Scribe" ] ]
        , div [ class "sermon-detail" ]
            [ div [ class "sermon-detail__header" ]
                [ div [ class "sermon-detail__navigation" ]
                    [ Icon.view "button" Icon.back False [ onClick CloseSermon ]
                    , viewAIWarning
                    ]
                , Card.view
                    (text (Maybe.withDefault "Title Unknown" sermon.title))
                    [ viewProcessingRetry model sermon "title" "Regenerate Title"
                    , case sermon.titleReasoning of
                        Just reasoning ->
                            viewIconDisclosure Icon.titleInfo reasoning

                        Nothing ->
                            text ""
                    , Icon.view "button"
                        Icon.deleteSermon
                        (Set.member sermon.id model.deleting)
                        [ onClick (AskDelete sermon)
                        , disabled (Set.member sermon.id model.deleting || Set.member sermon.id model.retrying || model.confirmingDelete /= Nothing)
                        ]
                    ]
                    [ p [ class "sermon-detail__filename" ] [ text sermon.originalFilename ]
                    , case sermon.speaker of
                        Just speaker ->
                            if String.isEmpty (String.trim speaker) then
                                text ""

                            else
                                p [ class "sermon-detail__speaker" ] [ text ("Speaker: " ++ speaker) ]

                        Nothing ->
                            text ""
                    , p [ Ui.cardMeta ]
                        [ span [ badgeAttribute sermon ] [ text (describeStage sermon) ]
                        , text (formatDate model.zone sermon.uploadedAt)
                        ]
                    , viewOptionalError model.deleteError
                    , viewOptionalError model.retryError
                    , viewDeleteConfirmation model sermon
                    ]
                ]
            , viewDetailStatus sermon
            , div
                [ class
                    (if hasMetadata then
                        "sermon-detail__columns"

                     else
                        "sermon-detail__columns sermon-detail__columns--single"
                    )
                ]
                [ div [ class "sermon-detail__column", hidden (not hasMetadata) ]
                    [ viewDetailMetadata model sermon ]
                , div [ class "sermon-detail__column" ]
                    [ viewDetailAudio sermon
                    , viewDetailTranscript model sermon
                    ]
                ]
            ]
        ]


viewAIWarning : Html Msg
viewAIWarning =
    viewIconDisclosure Icon.warning "This page includes AI-generated content to save you time. Please carefully verify the metadata before publishing."


viewIconDisclosure : Icon.Config -> String -> Html Msg
viewIconDisclosure config body =
    details [ Ui.iconDisclosure ]
        [ Icon.view "summary" config False []
        , div [ Ui.iconDisclosureContent ]
            [ div [ Ui.panel ]
                [ strong [ Ui.panelTitle ] [ text config.label ]
                , p [ Ui.panelText ] [ text body ]
                ]
            ]
        ]


viewDetailStatus : Sermon -> Html Msg
viewDetailStatus sermon =
    case sermon.status of
        "failed" ->
            div [ Ui.errorPanel ]
                [ strong [ Ui.panelTitle ] [ text "Processing failed" ]
                , p [ Ui.panelText ] [ text "Please regenerate or contact an administrator." ]
                ]

        "done" ->
            text ""

        _ ->
            text ""


viewDetailMetadata : Model -> Sermon -> Html Msg
viewDetailMetadata model sermon =
    div [ class "sermon-detail__sections" ]
        [ Card.view (text "Scripture References")
            [ viewProcessingRetry model sermon "scriptures" "Regenerate Scripture References" ]
            [ if List.isEmpty sermon.scriptures then
                p [ Ui.panelText ] [ text "No scripture references yet." ]

              else
                text ""
            , div [ class "sermon-detail__pills" ]
                (List.map viewScripture sermon.scriptures)
            ]
        , Card.view (text "Topics")
            [ viewProcessingRetry model sermon "topics" "Regenerate Topics" ]
            [ if List.isEmpty (highConfidenceTopics sermon.topicScores) then
                p [ Ui.panelText ] [ text "No high-confidence topics yet." ]

              else
                text ""
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


viewDetailAudio : Sermon -> Html Msg
viewDetailAudio sermon =
    let
        source =
            audioUrl sermon.id "playback"
    in
    Card.view (text "Audio")
        [ Icon.view "a"
            Icon.downloadAudio
            False
            [ href (source ++ "?download=1")
            , download ""
            ]
        ]
        [ audio [ class "sermon-detail__audio", controls True, attribute "preload" "metadata", src source ] [] ]


viewDetailTranscript : Model -> Sermon -> Html Msg
viewDetailTranscript model sermon =
    Card.view (text "Transcript")
        [ viewProcessingRetry model sermon "transcription" "Regenerate Transcription"
        , case sermon.transcript of
            Just transcript ->
                Icon.view "button"
                    (if model.transcriptCopyStatus == Just True then
                        Icon.copiedTranscript

                     else
                        Icon.copyTranscript
                    )
                    False
                    [ onClick (CopyTranscript transcript) ]

            Nothing ->
                text ""
        ]
        [ if model.transcriptCopyStatus == Just True then
            p [ Ui.panelText, attribute "role" "status" ] [ text "Transcript copied." ]

          else
            text ""
        , if model.transcriptCopyStatus == Just False then
            p [ Ui.errorText, attribute "role" "status" ] [ text "Could not copy. Please select the transcript and copy it manually." ]

          else
            text ""
        , case sermon.transcript of
            Just transcript ->
                let
                    matches =
                        if String.isEmpty model.transcriptSearch then
                            []

                        else
                            String.indexes (String.toLower model.transcriptSearch) (String.toLower transcript)

                    count =
                        List.length matches

                    ( offset, reversed ) =
                        List.indexedMap Tuple.pair matches
                            |> List.foldl
                                (\( index, start ) ( previousEnd, nodes ) ->
                                    ( start + String.length model.transcriptSearch
                                    , mark
                                        [ class
                                            (if index == model.transcriptMatch then
                                                "sermon-detail__match sermon-detail__match--active"

                                             else
                                                "sermon-detail__match"
                                            )
                                        , attribute "data-transcript-match" (String.fromInt index)
                                        ]
                                        [ text (String.slice start (start + String.length model.transcriptSearch) transcript) ]
                                        :: text (String.slice previousEnd start transcript)
                                        :: nodes
                                    )
                                )
                                ( 0, [] )
                in
                div []
                    [ div [ class "sermon-detail__search" ]
                        [ input
                            [ class "sermon-detail__search-input focusable"
                            , type_ "search"
                            , attribute "aria-label" "Search transcript"
                            , placeholder "Search transcript…"
                            , value model.transcriptSearch
                            , onInput SearchTranscript
                            ]
                            []
                        , if count > 0 then
                            div [ class "sermon-detail__search-navigation" ]
                                [ Icon.view "button" Icon.previousMatch False [ onClick (SelectTranscriptMatch (modBy count (model.transcriptMatch - 1))) ]
                                , span [ Ui.panelText, attribute "role" "status" ] [ text (String.fromInt (model.transcriptMatch + 1) ++ " of " ++ String.fromInt count) ]
                                , Icon.view "button" Icon.nextMatch False [ onClick (SelectTranscriptMatch (modBy count (model.transcriptMatch + 1))) ]
                                ]

                          else if not (String.isEmpty model.transcriptSearch) then
                            span [ Ui.panelText, attribute "role" "status" ] [ text "No matches" ]

                          else
                            text ""
                        ]
                    , div [ class "sermon-detail__transcript", attribute "tabindex" "0", attribute "aria-label" "Transcript", attribute "role" "region" ]
                        (List.reverse reversed ++ [ text (String.dropLeft offset transcript) ])
                    ]

            Nothing ->
                p [ Ui.panelText ] [ text "Transcript not available yet." ]
        ]


highConfidenceTopics : List ( String, Float ) -> List ( String, Float )
highConfidenceTopics scores =
    scores
        |> List.filter (\( _, score ) -> score >= 0.8)
        |> List.sortBy (\( _, score ) -> -score)


viewScripture : String -> Html Msg
viewScripture reference =
    span
        [ class "sermon-detail__pill sermon-detail__pill--scripture" ]
        [ text reference ]


viewProcessingRetry : Model -> Sermon -> String -> String -> Html Msg
viewProcessingRetry model sermon part label =
    let
        processing =
            Dict.get sermon.id model.regenerating
                == Just part
                || ((sermon.status == "pending" || sermon.status == "running")
                        && (sermon.stage
                                == part
                                || (sermon.stage == "metadata" && part /= "transcription")
                           )
                   )
    in
    Icon.view "button"
        (Icon.regenerate label)
        processing
        [ onClick (RetryProcessing sermon part)
        , disabled
            (Set.member sermon.id model.retrying
                || Set.member sermon.id model.deleting
                || Set.member sermon.id model.rerunning
                || Set.member sermon.id model.reviewingNormalization
                || model.confirmingDelete
                /= Nothing
                || (sermon.status /= "done" && sermon.status /= "failed")
                || sermon.stage
                == "upload"
                || (part /= "transcription" && sermon.transcript == Nothing)
            )
        ]


viewDeleteConfirmation : Model -> Sermon -> Html Msg
viewDeleteConfirmation model sermon =
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
                text ""

        Nothing ->
            text ""


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
            ]
        , Icon.view "button"
            Icon.openSermon
            False
            [ stopPropagationOn "click" (Decode.succeed ( OpenSermon sermon, True ))
            , disabled (model.confirmingDelete /= Nothing || Set.member sermon.id model.deleting)
            ]
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
                            "Saving review…"

                         else
                            "Complete Normalization Review"
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
