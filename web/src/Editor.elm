module Editor exposing (Effect(..), Model, Msg(..), init, pipeline, update, view)

import Api exposing (Region, Sermon, Timeline, Waveform)
import Dict exposing (Dict)
import Html exposing (Html, audio, button, canvas, div, h1, input, node, p, span, strong, text)
import Html.Attributes exposing (attribute, autofocus, class, controls, disabled, id, src, tabindex, title, type_, value)
import Html.Events exposing (on, onBlur, onClick, onInput)
import Http
import Json.Decode as Decode
import Json.Encode as Encode
import Timeline
import Ui


type alias Model =
    { sermon : Sermon
    , waveform : Maybe Waveform
    , analysis : Maybe Timeline
    , draft : Maybe Timeline
    , draftReceived : Bool
    , regions : List Region
    , selected : Int
    , boundaryEdits : Dict Int String
    , dirty : Bool
    , applying : Bool
    , finalReady : Bool
    , confirmingApproval : Bool
    , error : Maybe String
    , backWarning : Bool
    , playhead : Float
    , zoom : Float
    , viewStart : Float
    , audition : Maybe BoundaryAudition
    }


type BoundaryAudition
    = StartAudition
    | EndAudition


type Msg
    = GotWaveform (Result Http.Error Waveform)
    | GotAnalysis (Result Http.Error Timeline)
    | GotDraft Decode.Value
    | Select Int
    | Toggle Int
    | SetKeep Int Bool
    | Nudge Int Float
    | EditBoundary Int String
    | CommitBoundary Int
    | CanvasSelect Decode.Value
    | Playhead Decode.Value
    | ZoomIn
    | ZoomOut
    | ZoomToSelected
    | Pan Float
    | ShowAll
    | SelectPrevious
    | SelectNext
    | ToggleAudition BoundaryAudition
    | AuditionEnded Decode.Value
    | Apply
    | Applied (Result Http.Error Sermon)
    | ConfirmApproval
    | CancelApproval
    | Approve
    | Approved (Result Http.Error Sermon)
    | Back
    | ForceClose
    | KeepEditing


type Effect
    = None
    | LoadDraft String
    | SaveDraft String Encode.Value Encode.Value Encode.Value
    | ClearDraft String
    | Render Encode.Value Encode.Value
    | Complete String Encode.Value Encode.Value
    | Preview Encode.Value
    | Audition Encode.Value
    | Close
    | ApprovedSermon Sermon


init : Sermon -> ( Model, Cmd Msg, Effect )
init sermon =
    ( { sermon = sermon, waveform = Nothing, analysis = Nothing, draft = Nothing, draftReceived = False, regions = [], selected = 0, boundaryEdits = Dict.empty, dirty = False, applying = False, finalReady = sermon.stage == "edit" && sermon.status == "done", confirmingApproval = False, error = Nothing, backWarning = False, playhead = 0, zoom = 1, viewStart = 0, audition = Nothing }
    , Cmd.batch [ Api.waveform GotWaveform sermon.id, Api.analyze GotAnalysis sermon.id ]
    , LoadDraft sermon.id
    )


resolve : Model -> Model
resolve model =
    case ( model.waveform, model.analysis, model.draftReceived ) of
        ( Just wave, Just analyzed, True ) ->
            let
                validDraft =
                    model.draft
                        |> Maybe.andThen
                            (\draft ->
                                if abs (draft.duration - wave.duration) < 0.01 && valid wave.duration draft.regions then
                                    Just draft.regions

                                else
                                    Nothing
                            )

                chosen =
                    (validDraft
                        |> Maybe.withDefault
                            (if model.finalReady then
                                model.sermon.appliedRegions |> Maybe.withDefault analyzed.regions

                             else
                                analyzed.regions
                            )
                    )
                        |> Timeline.canonicalize
            in
            if List.isEmpty model.regions then
                { model | regions = chosen, dirty = validDraft /= Nothing, finalReady = model.finalReady && validDraft == Nothing }

            else
                model

        _ ->
            model


update : Msg -> Model -> ( Model, Cmd Msg, Effect )
update msg model =
    case msg of
        GotWaveform result ->
            case result of
                Ok wave ->
                    finish { model | waveform = Just wave }

                Err _ ->
                    ( { model | error = Just "Could not load the waveform. Please return and try again." }, Cmd.none, None )

        GotAnalysis result ->
            case result of
                Ok value ->
                    finish { model | analysis = Just value }

                Err _ ->
                    ( { model | error = Just "Could not analyze this recording. Please try again." }, Cmd.none, None )

        GotDraft value ->
            case Decode.decodeValue (Decode.map2 Tuple.pair (Decode.field "id" Decode.string) (Decode.field "draft" Decode.value)) value of
                Ok ( id, draftValue ) ->
                    if id == model.sermon.id then
                        case Decode.decodeValue (Decode.nullable Timeline.decoder) draftValue of
                            Ok draft ->
                                finish { model | draftReceived = True, draft = draft }

                            Err _ ->
                                finishDiscardDraft { model | draftReceived = True, draft = Nothing }

                    else
                        ( model, Cmd.none, None )

                Err _ ->
                    ( model, Cmd.none, None )

        Select index ->
            redraw (selectRegion index model)

        Toggle index ->
            if busy model then
                ( model, Cmd.none, None )

            else
                changed
                    { model
                        | regions =
                            List.indexedMap
                                (\i r ->
                                    if i == index then
                                        { r | keep = not r.keep }

                                    else
                                        r
                                )
                                model.regions
                        , selected = index
                    }

        SetKeep index keep ->
            if busy model then
                ( model, Cmd.none, None )

            else
                changed
                    { model
                        | regions =
                            List.indexedMap
                                (\i region ->
                                    if i == index then
                                        { region | keep = keep }

                                    else
                                        region
                                )
                                model.regions
                        , selected = index
                    }

        Nudge boundary delta ->
            if busy model then
                ( model, Cmd.none, None )

            else
                changeBoundary boundary (editableBoundaryTime boundary model + delta) model

        EditBoundary boundary inputValue ->
            ( { model | boundaryEdits = Dict.insert boundary inputValue model.boundaryEdits }, Cmd.none, None )

        CommitBoundary boundary ->
            if busy model then
                ( model, Cmd.none, None )

            else
                case Dict.get boundary model.boundaryEdits of
                    Nothing ->
                        ( model, Cmd.none, None )

                    Just inputValue ->
                        case parseTimecode inputValue of
                            Just time ->
                                changeBoundary boundary time model

                            Nothing ->
                                redraw
                                    { model
                                        | boundaryEdits = Dict.remove boundary model.boundaryEdits
                                        , error = Just "Enter a time as minutes:seconds, for example 0:07.04."
                                    }

        CanvasSelect value ->
            case Decode.decodeValue Decode.int value of
                Ok index ->
                    redraw (selectRegion index model)

                Err _ ->
                    ( model, Cmd.none, None )

        Playhead value ->
            case Decode.decodeValue Decode.float value of
                Ok time ->
                    ( { model | playhead = time }, Cmd.none, None )

                Err _ ->
                    ( model, Cmd.none, None )

        ZoomIn ->
            redraw (setZoom (model.zoom * 2) model)

        ZoomOut ->
            redraw (setZoom (model.zoom / 2) model)

        ZoomToSelected ->
            redraw (zoomToSelected model)

        Pan direction ->
            let
                span =
                    visibleSpan model

                next =
                    { model | viewStart = clampViewStart model (model.viewStart + direction * span * 0.75) }
            in
            redraw next

        ShowAll ->
            redraw { model | zoom = 1, viewStart = 0 }

        SelectPrevious ->
            redraw (selectRegion (model.selected - 1) model)

        SelectNext ->
            redraw (selectRegion (model.selected + 1) model)

        ToggleAudition requested ->
            if model.audition == Just requested then
                ( { model | audition = Nothing }, Cmd.none, Audition Encode.null )

            else
                case auditionRange requested model of
                    Just ( start, end ) ->
                        ( { model | audition = Just requested }
                        , Cmd.none
                        , Audition
                            (Encode.object
                                [ ( "start", Encode.float start )
                                , ( "end", Encode.float end )
                                ]
                            )
                        )

                    Nothing ->
                        ( model, Cmd.none, None )

        AuditionEnded _ ->
            ( { model | audition = Nothing }, Cmd.none, None )

        Apply ->
            if busy model then
                ( model, Cmd.none, None )

            else
                ( { model | applying = True, error = Nothing }, Api.applyEdits Applied model.sermon.id model.regions, None )

        Applied result ->
            case result of
                Ok _ ->
                    -- SSE snapshots and updates are authoritative. The queue can
                    -- finish before this older HTTP acknowledgement arrives.
                    ( model, Cmd.none, None )

                Err _ ->
                    ( { model | applying = False, error = Just "Could not queue the edit. Check the regions and try again." }, Cmd.none, None )

        ConfirmApproval ->
            ( { model | confirmingApproval = True }, Cmd.none, None )

        CancelApproval ->
            ( { model | confirmingApproval = False }, Cmd.none, None )

        Approve ->
            ( { model | applying = True }, Api.approveEdit Approved model.sermon.id, None )

        Approved result ->
            case result of
                Ok sermon ->
                    ( model, Cmd.none, ApprovedSermon sermon )

                Err _ ->
                    ( { model | applying = False, confirmingApproval = False, error = Just "Could not approve the final audio. Please try again." }, Cmd.none, None )

        Back ->
            if busy model then
                ( model, Cmd.none, None )

            else if model.dirty then
                ( { model | backWarning = True }, Cmd.none, None )

            else
                ( model, Cmd.none, Close )

        ForceClose ->
            ( model, Cmd.none, Close )

        KeepEditing ->
            ( { model | backWarning = False }, Cmd.none, None )


finish model =
    let
        ready =
            resolve model
    in
    ( ready
    , Cmd.none
    , if List.isEmpty ready.regions then
        None

      else
        renderEffect ready
    )


finishDiscardDraft model =
    let
        ( ready, command, effect ) =
            finish model
    in
    case effect of
        Render render preview ->
            ( ready, command, Complete model.sermon.id render preview )

        _ ->
            ( ready, command, ClearDraft model.sermon.id )


changed model =
    let
        next =
            { model | dirty = True, finalReady = False, error = Nothing }
    in
    case renderEffect next of
        Render render preview ->
            ( next, Cmd.none, SaveDraft next.sermon.id (encodeTimeline next) render preview )

        _ ->
            ( next, Cmd.none, SaveDraft next.sermon.id (encodeTimeline next) Encode.null (previewValue next) )


changeBoundary boundary time model =
    changed
        { model
            | regions = Timeline.changeBoundary boundary time model.regions
            , boundaryEdits = Dict.remove boundary model.boundaryEdits
            , audition = Nothing
        }


pipeline sermon model =
    if sermon.id /= model.sermon.id then
        ( model, None )

    else if sermon.editApproved then
        ( model, ApprovedSermon sermon )

    else if sermon.stage == "edit" && sermon.status == "done" then
        case sermon.appliedRegions of
            Just regions ->
                if model.waveform == Nothing || model.analysis == Nothing || not model.draftReceived then
                    -- Resolve the local draft before deciding whether this is a
                    -- new completion or a reconnect describing an older render.
                    ( { model | sermon = sermon, applying = False, finalReady = True }, None )

                else if model.dirty && model.regions /= regions then
                    -- A reconnect snapshot describes the last successful render,
                    -- not necessarily the user's newer local draft.
                    ( { model | sermon = sermon, applying = False }, None )

                else
                    let
                        next =
                            { model | sermon = sermon, regions = regions, applying = False, finalReady = True, dirty = False, error = Nothing }
                    in
                    case renderEffect next of
                        Render render preview ->
                            ( next, Complete sermon.id render preview )

                        _ ->
                            ( next, ClearDraft sermon.id )

            Nothing ->
                ( { model | sermon = sermon, applying = False, error = Just "The completed edit did not include its persisted regions." }, None )

    else if sermon.stage == "edit" && sermon.status == "failed" then
        ( { model | sermon = sermon, applying = False, error = sermon.error }, None )

    else
        ( { model | sermon = sermon, applying = sermon.stage == "edit" && (sermon.status == "pending" || sermon.status == "running") }, None )


valid duration regions =
    Timeline.valid duration regions


get index list =
    List.drop index list |> List.head


encodeRegions =
    Timeline.encodeRegions


encodeTimeline model =
    Encode.object [ ( "duration", Encode.float (model.waveform |> Maybe.map .duration |> Maybe.withDefault 0) ), ( "regions", encodeRegions model.regions ) ]


previewValue model =
    Encode.object
        [ ( "mode"
          , Encode.string
                (if model.finalReady then
                    "final"

                 else
                    "preview"
                )
          )
        , ( "regions", encodeRegions model.regions )
        ]


renderEffect model =
    case model.waveform of
        Just wave ->
            Render
                (Encode.object
                    [ ( "duration", Encode.float wave.duration )
                    , ( "samples", Encode.list Encode.float wave.samples )
                    , ( "regions", encodeRegions model.regions )
                    , ( "playhead", Encode.float model.playhead )
                    , ( "selected", Encode.int model.selected )
                    , ( "view_start", Encode.float model.viewStart )
                    , ( "view_end", Encode.float (model.viewStart + visibleSpan model) )
                    ]
                )
                (previewValue model)

        Nothing ->
            None


view model =
    div [ class "editor" ]
        [ div [ class "editor__header" ]
            [ timelineIconButton "Back to sermons" "mdi:arrow-left" Back (busy model)
            , div []
                [ h1 [ class "editor__title" ] [ text "Edit audio" ]
                , p [ class "editor__filename" ] [ text model.sermon.originalFilename ]
                ]
            ]
        , viewBody model
        ]


viewBody model =
    if loadFailed model then
        div [ class "editor__load-error" ]
            [ viewError model.error
            , button [ Ui.button, onClick ForceClose ] [ text "Back to Sermons" ]
            ]

    else if model.waveform == Nothing || model.analysis == Nothing || not model.draftReceived then
        p [ class "editor__loading" ] [ text "Loading waveform and finding sections…" ]

    else
        div []
            [ viewError model.error
            , div [ class "timeline-stage" ]
                [ canvas
                    [ id "timeline-canvas"
                    , class "timeline focusable"
                    , attribute "role" "img"
                    , attribute "aria-label" "Read-only audio waveform. Tap a section to select it for editing below."
                    , attribute "aria-readonly" "true"
                    , attribute "data-busy"
                        (if busy model then
                            "true"

                         else
                            "false"
                        )
                    , tabindex 0
                    ]
                    []
                ]
            , if model.finalReady then
                text ""

              else
                viewAudioPreview model
            , viewStatus model
            , viewRegionInspector model
            , if model.finalReady then
                viewAudioPreview model

              else
                text ""
            , div [ class "editor__actions" ]
                [ button [ Ui.primaryButton, onClick Apply, disabled (busy model || model.finalReady || not (validPlan model)) ]
                    [ text
                        (if model.applying then
                            "Rendering…"

                         else
                            "Apply Edits"
                        )
                    ]
                , if model.finalReady then
                    button [ Ui.dangerButton, onClick ConfirmApproval, disabled model.applying ] [ text "Approve Final Audio" ]

                  else
                    text ""
                , button [ Ui.button, onClick Back, disabled (busy model) ] [ text "Back to Sermons" ]
                ]
            , approval model
            , backWarning model
            ]


validPlan model =
    valid (model.waveform |> Maybe.map .duration |> Maybe.withDefault 0) model.regions && List.any .keep model.regions


viewAudioPreview model =
    div [ class "audio-preview" ]
        [ strong [ class "audio-preview__label" ]
            [ text
                (if model.finalReady then
                    "Final audio"

                 else
                    "Edit preview"
                )
            ]
        , audio [ id "timeline-audio", class "editor__audio", controls True, src (audioSource model) ] []
        ]


audioSource model =
    if model.finalReady then
        "/api/sermons/" ++ model.sermon.id ++ "/audio/final?v=" ++ String.fromInt model.sermon.progress

    else
        proxyAudioSource model


proxyAudioSource model =
    "/api/sermons/"
        ++ model.sermon.id
        ++ "/audio/proxy?gate="
        ++ String.fromInt model.sermon.normalizationGateAdjustment
        ++ "&volume="
        ++ String.fromInt model.sermon.normalizationVolumeAdjustment


viewStatus model =
    if model.applying then
        p [ class "editor__status" ] [ text ("Rendering final audio… " ++ String.fromInt model.sermon.progress ++ "%") ]

    else
        text ""


viewError error =
    error |> Maybe.map (\e -> p [ Ui.errorText ] [ text e ]) |> Maybe.withDefault (text "")


approval model =
    if model.confirmingApproval then
        div [ Ui.confirmBox ] [ p [ Ui.confirmBoxQuestion ] [ strong [] [ text "This permanently deletes the raw recording. You won't be able to re-edit this sermon. Approve?" ] ], div [ Ui.confirmBoxButtons ] [ button [ Ui.dangerButton, onClick Approve, disabled model.applying ] [ text "Yes, Approve Permanently" ], button [ Ui.button, onClick CancelApproval ] [ text "Cancel" ] ] ]

    else
        text ""


backWarning model =
    if model.backWarning then
        div
            [ class "editor-modal"
            , attribute "role" "dialog"
            , attribute "aria-modal" "true"
            , attribute "aria-labelledby" "leave-editor-title"
            , attribute "aria-describedby" "leave-editor-description"
            , on "keydown" escapeDecoder
            ]
            [ div [ class "editor-modal__dialog" ]
                [ strong [ class "editor-modal__title", id "leave-editor-title" ] [ text "Leave without applying changes?" ]
                , p [ class "editor-modal__description", id "leave-editor-description" ] [ text "Your draft is saved in this browser, but these changes have not been applied to the final audio." ]
                , div [ class "editor-modal__actions" ]
                    [ button [ Ui.dangerButton, onClick ForceClose ] [ text "Leave Editor" ]
                    , button [ Ui.primaryButton, onClick KeepEditing, autofocus True ] [ text "Keep Editing" ]
                    ]
                ]
            ]

    else
        text ""


escapeDecoder =
    Decode.field "key" Decode.string
        |> Decode.andThen
            (\key ->
                if key == "Escape" then
                    Decode.succeed KeepEditing

                else
                    Decode.fail "not escape"
            )


viewRegionInspector model =
    case get model.selected model.regions of
        Nothing ->
            text ""

        Just region ->
            div [ class "region-inspector" ]
                [ div [ class "region-inspector__nav" ]
                    [ timelineIconButton "Previous section" "mdi:chevron-left" SelectPrevious (model.selected <= 0)
                    , div [ class "region-inspector__identity" ]
                        [ strong [ class "region-inspector__title" ]
                            [ text (regionLabel region.regionType) ]
                        , span [ class "region-inspector__subtitle" ]
                            [ text ("Section " ++ String.fromInt (model.selected + 1) ++ "/" ++ String.fromInt (List.length model.regions)) ]
                        , span [ class "region-inspector__time" ]
                            [ text (Timeline.timestamp region.start ++ " – " ++ Timeline.timestamp region.end) ]
                        ]
                    , timelineIconButton "Next section" "mdi:chevron-right" SelectNext (model.selected >= List.length model.regions - 1)
                    ]
                , div [ class "region-inspector__view" ]
                    [ button [ Ui.button, onClick ZoomToSelected, disabled (busy model) ] [ text "Zoom to section" ]
                    , if model.zoom > 1 then
                        button [ Ui.button, onClick ShowAll, disabled (busy model) ] [ text "Show full audio" ]

                      else
                        text ""
                    ]
                , div [ class "region-inspector__decision" ]
                    [ decisionButton "Keep" "mdi:check" (SetKeep model.selected True) region.keep (busy model)
                    , decisionButton "Delete" "mdi:content-cut" (SetKeep model.selected False) (not region.keep) (busy model)
                    ]
                , div [ class "precision-editor" ]
                    [ if model.selected > 0 then
                        boundaryRow model "Start" model.selected region.start StartAudition

                      else
                        text ""
                    , if model.selected < List.length model.regions - 1 then
                        boundaryRow model "End" (model.selected + 1) region.end EndAudition

                      else
                        text ""
                    , audio
                        [ id "boundary-audio"
                        , attribute "aria-hidden" "true"
                        , attribute "preload" "auto"
                        , src (proxyAudioSource model)
                        ]
                        []
                    ]
                ]


regionLabel regionType =
    case regionType of
        "speaking" ->
            "Speech"

        "singing" ->
            "Singing"

        _ ->
            "Silence"


decisionButton label iconName message selected isBusy =
    button
        [ class
            (if selected then
                "decision-button decision-button--selected"

             else
                "decision-button"
            )
        , onClick message
        , disabled (isBusy || selected)
        , attribute "aria-pressed"
            (if selected then
                "true"

             else
                "false"
            )
        ]
        [ icon iconName
        , text label
        ]


boundaryRow model label boundary time audition =
    div [ class "precision-editor__row" ]
        [ span [ class "precision-editor__label" ] [ text label ]
        , div [ class "precision-editor__controls" ]
            [ div [ class "precision-editor__nudge-group" ]
                [ nudgeButton model label boundary -1
                , nudgeButton model label boundary -0.1
                , nudgeButton model label boundary -0.01
                ]
            , input
                [ class "precision-editor__input focusable"
                , type_ "text"
                , attribute "inputmode" "decimal"
                , attribute "aria-label" (label ++ " time")
                , value (Dict.get boundary model.boundaryEdits |> Maybe.withDefault (Timeline.timestamp time))
                , onInput (EditBoundary boundary)
                , onBlur (CommitBoundary boundary)
                , disabled (busy model)
                ]
                []
            , div [ class "precision-editor__nudge-group" ]
                [ nudgeButton model label boundary 0.01
                , nudgeButton model label boundary 0.1
                , nudgeButton model label boundary 1
                ]
            ]
        , auditionButton model audition
        ]


auditionButton model requested =
    let
        active =
            model.audition == Just requested

        label =
            if active then
                "Stop preview"

            else
                "Preview"

        accessibleLabel =
            case requested of
                StartAudition ->
                    "Preview first 1 second"

                EndAudition ->
                    "Preview last 1 second"
    in
    button
        [ Ui.button
        , class "precision-editor__audition"
        , onClick (ToggleAudition requested)
        , disabled (busy model)
        , attribute "aria-label"
            (if active then
                "Stop boundary preview"

             else
                accessibleLabel
            )
        , attribute "aria-pressed"
            (if active then
                "true"

             else
                "false"
            )
        ]
        [ icon
            (if active then
                "mdi:stop"

             else
                "mdi:play"
            )
        , text label
        ]


auditionRange requested model =
    get model.selected model.regions
        |> Maybe.map
            (\region ->
                case requested of
                    StartAudition ->
                        ( region.start, min region.end (region.start + 1) )

                    EndAudition ->
                        ( max region.start (region.end - 1), region.end )
            )


nudgeButton model label boundary amount =
    let
        amountLabel =
            (if amount < 0 then
                "−"

             else
                "+"
            )
                ++ String.fromFloat (abs amount)
                ++ "s"
    in
    button
        [ class "boundary-button"
        , on "pointerdown" (Decode.succeed (Nudge boundary amount))
        , on "click" (keyboardClickDecoder (Nudge boundary amount))
        , disabled (busy model || nudgeAtLimit boundary amount model)
        , attribute "aria-label" ("Move " ++ String.toLower label ++ " by " ++ amountLabel)
        , title ("Move " ++ String.toLower label ++ " by " ++ amountLabel)
        ]
        [ text amountLabel ]


keyboardClickDecoder message =
    Decode.field "detail" Decode.int
        |> Decode.andThen
            (\detail ->
                if detail == 0 then
                    Decode.succeed message

                else
                    Decode.fail "pointer click handled on pointerdown"
            )


editableBoundaryTime boundary model =
    Dict.get boundary model.boundaryEdits
        |> Maybe.andThen parseTimecode
        |> Maybe.withDefault (Timeline.boundaryTime boundary model.regions)


nudgeAtLimit boundary amount model =
    case ( get (boundary - 1) model.regions, get boundary model.regions ) of
        ( Just left, Just right ) ->
            let
                time =
                    editableBoundaryTime boundary model
            in
            if amount < 0 then
                time <= left.start + 0.010001

            else
                time >= right.end - 0.010001

        _ ->
            True


parseTimecode inputValue =
    case String.split ":" (String.trim inputValue) of
        [ minutesText, secondsText ] ->
            case ( String.toInt minutesText, String.toFloat secondsText ) of
                ( Just minutes, Just seconds ) ->
                    if minutes >= 0 && seconds >= 0 && seconds < 60 then
                        Just (toFloat minutes * 60 + seconds)

                    else
                        Nothing

                _ ->
                    Nothing

        [ secondsText ] ->
            String.toFloat secondsText
                |> Maybe.andThen
                    (\seconds ->
                        if seconds >= 0 then
                            Just seconds

                        else
                            Nothing
                    )

        _ ->
            Nothing


busy model =
    model.applying || (model.sermon.stage == "edit" && (model.sermon.status == "pending" || model.sermon.status == "running"))


loadFailed model =
    model.error /= Nothing && (model.waveform == Nothing || model.analysis == Nothing)


timelineIconButton label iconName message isDisabled =
    button
        [ Ui.iconButton
        , onClick message
        , disabled isDisabled
        , attribute "aria-label" label
        , title label
        ]
        [ icon iconName ]


icon iconName =
    node "iconify-icon"
        [ class "button__icon"
        , attribute "icon" iconName
        , attribute "width" "24"
        , attribute "height" "24"
        , attribute "aria-hidden" "true"
        ]
        []


redraw model =
    ( model, Cmd.none, renderEffect model )


selectRegion requested model =
    let
        index =
            max 0 (min (List.length model.regions - 1) requested)

        selected =
            get index model.regions

        span =
            visibleSpan model

        start =
            selected
                |> Maybe.andThen
                    (\region ->
                        if region.start < model.viewStart || region.end > model.viewStart + span then
                            Just (clampViewStart model ((region.start + region.end - span) / 2))

                        else
                            Nothing
                    )
                |> Maybe.withDefault model.viewStart
    in
    { model | selected = index, boundaryEdits = Dict.empty, viewStart = start, audition = Nothing }


zoomToSelected model =
    case ( model.waveform, get model.selected model.regions ) of
        ( Just waveform, Just region ) ->
            let
                padding =
                    max 0.5 ((region.end - region.start) * 0.1)

                focusStart =
                    max 0 (region.start - padding)

                focusEnd =
                    min waveform.duration (region.end + padding)

                focusSpan =
                    max 0.01 (focusEnd - focusStart)

                next =
                    { model | zoom = max 1 (min 512 (waveform.duration / focusSpan)) }
            in
            { next | viewStart = clampViewStart next ((focusStart + focusEnd - visibleSpan next) / 2) }

        _ ->
            model


setZoom requested model =
    let
        duration =
            model.waveform |> Maybe.map .duration |> Maybe.withDefault 0

        oldSpan =
            visibleSpan model

        center =
            model.viewStart + oldSpan / 2

        zoom =
            max 1 (min 512 requested)

        newSpan =
            if zoom <= 0 then
                duration

            else
                duration / zoom

        next =
            { model | zoom = zoom }
    in
    { next | viewStart = clampViewStart next (center - newSpan / 2) }


visibleSpan model =
    let
        duration =
            model.waveform |> Maybe.map .duration |> Maybe.withDefault 0
    in
    if model.zoom <= 0 then
        duration

    else
        duration / model.zoom


clampViewStart model requested =
    let
        duration =
            model.waveform |> Maybe.map .duration |> Maybe.withDefault 0
    in
    max 0 (min (max 0 (duration - visibleSpan model)) requested)
