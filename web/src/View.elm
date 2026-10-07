module View exposing (view)

import Api exposing (Sermon)
import Button
import Card
import DateFormat exposing (formatDate)
import Dict
import Editing
import File
import Html exposing (Html, a, audio, button, div, h2, input, label, mark, p, span, strong, text)
import Html.Attributes exposing (accept, attribute, checked, class, classList, controls, disabled, download, hidden, href, id, placeholder, src, style, title, type_, value)
import Html.Events exposing (on, onCheck, onClick, onInput, stopPropagationOn)
import Json.Decode as Decode exposing (Decoder)
import Set
import Types exposing (Model, Msg(..), SermonList(..), UploadState(..))
import Ui
import Url


view : Model -> Html Msg
view model =
    case model.selectedSermon of
        Just sermonId ->
            case model.sermons of
                Loaded sermons ->
                    case List.filter (\sermon -> sermon.id == sermonId) sermons |> List.head of
                        Just sermon ->
                            viewSermonDetail model sermon

                        Nothing ->
                            viewDetailMessage "Sermon not found."

                Loading ->
                    viewDetailMessage "Loading sermon…"

                LoadFailed ->
                    viewDetailMessage "Could not load the sermon. Please reload the page."

        Nothing ->
            div [ class "page" ]
                [ viewUpload model.upload
                , h2 [] [ text "Sermons" ]
                , viewOptionalError model.deleteError
                , viewSermons model
                ]


viewDetailMessage : String -> Html Msg
viewDetailMessage message =
    div [ class "page page--detail" ]
        [ div [ class "sermon-detail" ]
            [ div [ class "sermon-detail__header" ]
                [ div [ class "sermon-detail__navigation" ]
                    [ Button.view "a" Button.back False [ href "/" ]
                    , viewAIWarning
                    ]
                , Card.view (text "Sermon") [] [ p [] [ text message ] ]
                ]
            ]
        ]


viewSermonDetail : Model -> Sermon -> Html Msg
viewSermonDetail model sermon =
    let
        hasMetadata =
            sermon.title
                /= Nothing
                || sermon.titleReasoning
                /= Nothing
                || not (List.isEmpty sermon.scriptures)
                || not (List.isEmpty (highConfidenceTopics sermon.topicScores))
    in
    div [ class "page page--detail" ]
        [ div [ class "sermon-detail" ]
            [ div [ class "sermon-detail__header" ]
                [ div [ class "sermon-detail__navigation" ]
                    [ Button.view "a" Button.back False [ href "/" ]
                    , viewAIWarning
                    ]
                , Card.view
                    (text (Maybe.withDefault "Title Unknown" sermon.title))
                    [ viewProcessingRetry model sermon "title" "Regenerate Title"
                    , case sermon.titleReasoning of
                        Just reasoning ->
                            Button.disclosure Button.titleInfo reasoning

                        Nothing ->
                            text ""
                    , Button.view "button"
                        Button.deleteSermon
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
            , viewDetailStatus model sermon
            , if Editing.isOpen sermon.id model.editor then
                Html.map EditingMsg (Editing.view model.editor)

              else
                text ""
            , if Editing.isOpen sermon.id model.editor then
                text ""

              else
                div
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
                        [ viewDetailAudio model sermon
                        , viewDetailTranscript model sermon
                        ]
                    ]
            ]
        ]


viewAIWarning : Html Msg
viewAIWarning =
    Button.disclosure Button.warning "This page includes AI-generated content to save you time. Please carefully verify the metadata before publishing."


viewDetailStatus : Model -> Sermon -> Html Msg
viewDetailStatus model sermon =
    case sermon.status of
        "failed" ->
            div [ Ui.errorPanel ]
                [ strong [ Ui.panelTitle ] [ text "Processing failed" ]
                , p [ Ui.panelText ] [ text (Maybe.withDefault "Please try again or contact an administrator." sermon.error) ]
                , button [ Button.button, onClick (RetryProcessing sermon ""), disabled (Set.member sermon.id model.retrying) ] [ text "Retry processing" ]
                ]

        "done" ->
            text ""

        _ ->
            text ""


viewDetailMetadata : Model -> Sermon -> Html Msg
viewDetailMetadata model sermon =
    let
        options =
            Api.scriptureOptions sermon

        selected =
            Dict.get sermon.id model.scriptureDrafts
                |> Maybe.withDefault (Set.fromList sermon.scriptures)
    in
    div [ class "sermon-detail__sections" ]
        [ Card.view (text "Scripture References")
            [ viewProcessingRetry model sermon "scriptures" "Regenerate Scripture References" ]
            [ if List.isEmpty options then
                p [ Ui.panelText ] [ text "No scripture references found yet." ]

              else if List.isEmpty sermon.scriptures then
                p [ Ui.panelText ] [ text "No references selected yet. Select any additional passages below." ]

              else
                text ""
            , if List.isEmpty options then
                text ""

              else
                div [ class "sermon-detail__scripture-selections" ]
                    (List.map (viewScriptureOption sermon.id selected) options)
            , if Set.member sermon.id model.scriptureSaveErrors then
                div [ class "sermon-detail__scripture-save-error" ]
                    [ p [ class "sermon-detail__scripture-status" ] [ text "Could not save your selection." ]
                    , button [ Button.button, onClick (RetryScriptureSave sermon) ] [ text "Retry" ]
                    ]

              else
                text ""
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


viewScriptureOption : String -> Set.Set String -> String -> Html Msg
viewScriptureOption sermonId selected reference =
    label
        [ classList
            [ ( "sermon-detail__scripture-option", True )
            , ( "sermon-detail__scripture-option--selected", Set.member reference selected )
            ]
        ]
        [ input
            [ type_ "checkbox"
            , checked (Set.member reference selected)
            , onCheck (\_ -> ToggleScripture sermonId reference)
            ]
            []
        , span [] [ text reference ]
        ]


viewDetailAudio : Model -> Sermon -> Html Msg
viewDetailAudio model sermon =
    let
        source =
            audioUrl sermon.id "playback" ++ "?v=" ++ Url.percentEncode sermon.playbackVersion

        editable =
            (sermon.stage == "editing" && sermon.status == "done")
                || (sermon.stage == "metadata" && (sermon.status == "done" || sermon.status == "failed"))

        ownsDraft =
            model.editor.sermonId == Just sermon.id

        busy =
            ownsDraft && (model.editor.regenerating || model.editor.applying || model.editor.pendingApply /= Nothing)

        applying =
            (ownsDraft && (model.editor.applying || model.editor.pendingApply /= Nothing))
                || (sermon.stage == "editing" && (sermon.status == "pending" || sermon.status == "running"))

        duration =
            case
                if ownsDraft then
                    model.editor.draft

                else
                    Nothing
            of
                Just draft ->
                    Just (Editing.keptDuration draft)

                Nothing ->
                    sermon.editingDuration
    in
    Card.view (text "Audio")
        [ if duration |> Maybe.map (\seconds -> seconds > 0 && seconds < 90 * 60) |> Maybe.withDefault False then
            Button.primaryAction "ph:check"
                "Apply edits"
                busy
                [ onClick (EditingMsg (Editing.ApplyRecording sermon.id))
                , disabled (not editable || busy)
                ]

          else
            text ""
        , Button.view "button"
            Button.editAudio
            False
            [ onClick (EditingMsg (Editing.Open sermon.id))
            , disabled (not editable || busy)
            ]
        , Button.view "a"
            Button.downloadAudio
            False
            [ href (source ++ "&download=1")
            , download ""
            ]
        ]
        [ if applying then
            div [ class "sermon-detail__audio-loading", attribute "role" "status", attribute "aria-label" "Applying edits" ] [ Button.spinner ]

          else
            audio [ class "sermon-detail__audio", controls True, attribute "preload" "metadata", src source ] []
        , if duration |> Maybe.map (\seconds -> seconds >= 80 * 60) |> Maybe.withDefault False then
            p [ Ui.hint ] [ text "Please edit the audio to under 80 minutes." ]

          else
            text ""
        , if ownsDraft then
            viewOptionalError model.editor.error

          else
            text ""
        ]


viewDetailTranscript : Model -> Sermon -> Html Msg
viewDetailTranscript model sermon =
    Card.view (text "Transcript")
        [ viewProcessingRetry model sermon "transcription" "Regenerate Transcription"
        , case sermon.transcript of
            Just transcript ->
                Button.view "button"
                    (if model.transcriptCopyStatus == Just True then
                        Button.copiedTranscript

                     else
                        Button.copyTranscript
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
                                [ Button.view "button" Button.previousMatch False [ onClick (SelectTranscriptMatch (modBy count (model.transcriptMatch - 1))) ]
                                , span [ Ui.panelText, attribute "role" "status" ] [ text (String.fromInt (model.transcriptMatch + 1) ++ " of " ++ String.fromInt count) ]
                                , Button.view "button" Button.nextMatch False [ onClick (SelectTranscriptMatch (modBy count (model.transcriptMatch + 1))) ]
                                ]

                          else if not (String.isEmpty model.transcriptSearch) then
                            span [ Ui.panelText, attribute "role" "status" ] [ text "No matches" ]

                          else
                            text ""
                        ]
                    , div [ class "sermon-detail__transcript", attribute "tabindex" "0", attribute "aria-label" "Transcript", attribute "role" "region" ]
                        (case sermon.transcriptionMetadata of
                            Just metadata ->
                                if not (List.isEmpty metadata.segments) && String.isEmpty model.transcriptSearch then
                                    List.map viewTranscriptSegment metadata.segments

                                else
                                    List.reverse reversed ++ [ text (String.dropLeft offset transcript) ]

                            Nothing ->
                                List.reverse reversed ++ [ text (String.dropLeft offset transcript) ]
                        )
                    ]

            Nothing ->
                p [ Ui.panelText ] [ text "Transcript not available yet." ]
        ]


viewTranscriptSegment : Api.TranscriptSegment -> Html Msg
viewTranscriptSegment segment =
    div [ class "sermon-detail__transcript-segment" ]
        [ div [ class "sermon-detail__transcript-segment-meta" ]
            [ span [ class "sermon-detail__transcript-timestamp" ]
                [ text (formatTimestamp segment.start ++ "–" ++ formatTimestamp segment.end) ]
            , case segment.speaker of
                Just speaker ->
                    span [ class "sermon-detail__transcript-speaker" ] [ text ("Speaker " ++ String.fromInt (speaker + 1)) ]

                Nothing ->
                    text ""
            ]
        , p [ class "sermon-detail__transcript-segment-text" ] [ text segment.text ]
        ]


formatTimestamp : Float -> String
formatTimestamp seconds =
    let
        totalSeconds =
            max 0 (floor seconds)

        hours =
            totalSeconds // 3600

        minutes =
            modBy 3600 totalSeconds // 60

        remainder =
            modBy 60 totalSeconds

        padded value =
            String.padLeft 2 '0' (String.fromInt value)
    in
    if hours > 0 then
        String.fromInt hours ++ ":" ++ padded minutes ++ ":" ++ padded remainder

    else
        padded minutes ++ ":" ++ padded remainder


highConfidenceTopics : List ( String, Float ) -> List ( String, Float )
highConfidenceTopics scores =
    scores
        |> List.filter (\( _, score ) -> score >= 0.8)
        |> List.sortBy (\( _, score ) -> -score)


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
    Button.view "button"
        (Button.regenerate label)
        processing
        [ onClick (RetryProcessing sermon part)
        , disabled
            (Set.member sermon.id model.retrying
                || Set.member sermon.id model.deleting
                || model.confirmingDelete
                /= Nothing
                || (sermon.status /= "done" && sermon.status /= "failed")
                || sermon.stage
                == "upload"
                || (sermon.stage == "editing" && part /= "transcription")
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
                        [ button [ Button.dangerButton, onClick (ConfirmDelete sermon) ] [ text "Yes, Delete" ]
                        , button [ Button.button, onClick CancelDelete ] [ text "Cancel" ]
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
                    [ [ label [ Button.primaryButton, Html.Attributes.for "file-input" ]
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
        , Button.view "a"
            Button.openSermon
            False
            [ href ("/sermons/" ++ sermon.id)
            , stopPropagationOn "click" (Decode.succeed ( NoOp, True ))
            ]
        ]


audioUrl : String -> String -> String
audioUrl id audioType =
    "/api/sermons/" ++ id ++ "/audio/" ++ audioType


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

        ( "editing", "done" ) ->
            "Awaiting editing"

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
